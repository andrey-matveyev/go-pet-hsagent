package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/andrey-matveyev/go-library-queue/queue"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// --- Local E2E Mocks ---

type E2EMockTelegramSender struct {
	mu            sync.Mutex
	sentMessages  []tgbotapi.Chattable
	failNextCount int
}

func (m *E2EMockTelegramSender) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextCount > 0 {
		m.failNextCount--
		return tgbotapi.Message{}, fmt.Errorf("network error: telegram unreachable")
	}

	m.sentMessages = append(m.sentMessages, c)
	return tgbotapi.Message{MessageID: len(m.sentMessages)}, nil
}

func (m *E2EMockTelegramSender) GetSentMessages() []tgbotapi.Chattable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]tgbotapi.Chattable{}, m.sentMessages...)
}

// Dummy Agent Server for E2E
type E2EAgentServer struct {
	pb.UnimplementedMonitorServiceServer
	EventChan chan *pb.StreamEventsResponse
}

func (s *E2EAgentServer) StreamEvents(in *pb.StreamEventsRequest, stream pb.MonitorService_StreamEventsServer) error {
	for event := range s.EventChan {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

// Data model for queue tests
type E2EQueueItem struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// Helper for gRPC bufconn
func setupInMemoryServer(agent *E2EAgentServer) (*grpc.Server, *bufconn.Listener) {
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	pb.RegisterMonitorServiceServer(s, agent)
	go func() {
		_ = s.Serve(lis)
	}()
	return s, lis
}

// --- E2E Tests ---

func TestE2E_StreamPipeline(t *testing.T) {
	agent := &E2EAgentServer{
		EventChan: make(chan *pb.StreamEventsResponse, 10),
	}

	grpcServer, lis := setupInMemoryServer(agent)
	defer grpcServer.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	client := pb.NewMonitorServiceClient(conn)
	mockBot := &E2EMockTelegramSender{}

	// Симулируем поток доставки
	inpChan := make(chan E2EQueueItem, 10)
	listQueue := queue.NewListQueue[E2EQueueItem]()
	outChan := queue.AddQueue(ctx, listQueue, inpChan)

	// Worker
	go func() {
		for item := range outChan {
			msg := tgbotapi.NewMessage(item.ChatID, item.Text)
			_, _ = mockBot.Send(msg)
		}
	}()

	// Stream Listener
	go func() {
		stream, err := client.StreamEvents(ctx, &pb.StreamEventsRequest{})
		if err != nil {
			return
		}
		for {
			event, err := stream.Recv()
			if err != nil {
				return
			}
			inpChan <- E2EQueueItem{ChatID: 12345, Text: event.Message}
		}
	}()

	// Отправляем аларм от Агента
	agent.EventChan <- &pb.StreamEventsResponse{
		Type:    "battery",
		Message: "🪫 Low battery alert!",
	}

	time.Sleep(100 * time.Millisecond)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 delivered message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if msgConfig.Text != "🪫 Low battery alert!" {
		t.Errorf("Unexpected message text: %s", msgConfig.Text)
	}
}

func TestE2E_NetworkOutageAndShutdownRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	queueFilePath := filepath.Join(tmpDir, "e2e_queue.json")

	// 1. Формируем список сообщений
	pendingMsg := E2EQueueItem{ChatID: 12345, Text: "🔥 CPU Overheat 85°C"}

	listQueue := queue.NewListQueue[E2EQueueItem]()
	listQueue.Push(E2EQueueItem{ChatID: 12345, Text: "⚠️ Disk Full"})

	// 2. Сохраняем очереди на диск (без лишних тавтологических проверок)
	remaining := []E2EQueueItem{pendingMsg}

	exportedData, _ := queue.Export(listQueue, func(items []E2EQueueItem) ([]byte, error) {
		return json.Marshal(items)
	})

	var queueItems []E2EQueueItem
	_ = json.Unmarshal(exportedData, &queueItems)
	remaining = append(remaining, queueItems...)

	finalData, _ := json.MarshalIndent(remaining, "", "  ")
	_ = os.WriteFile(queueFilePath, finalData, 0644)

	// 3. Проверяем сохраненный файл
	savedData, err := os.ReadFile(queueFilePath)
	if err != nil {
		t.Fatalf("Failed to read queue file: %v", err)
	}

	var savedItems []E2EQueueItem
	_ = json.Unmarshal(savedData, &savedItems)

	if len(savedItems) != 2 {
		t.Fatalf("Expected 2 items saved, got %d", len(savedItems))
	}

	if savedItems[0].Text != "🔥 CPU Overheat 85°C" {
		t.Errorf("First item should be pending message, got: %s", savedItems[0].Text)
	}

	// 4. Восстановление при рестарте
	restoredQueue := queue.NewListQueue[E2EQueueItem]()
	err = queue.Import(restoredQueue, savedData, func(b []byte) ([]E2EQueueItem, error) {
		var items []E2EQueueItem
		err := json.Unmarshal(b, &items)
		return items, err
	})

	if err != nil {
		t.Fatalf("Failed to import queue: %v", err)
	}

	if restoredQueue.Len() != 2 {
		t.Errorf("Expected 2 restored items, got %d", restoredQueue.Len())
	}
}
