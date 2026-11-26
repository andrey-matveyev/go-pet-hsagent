package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// --- Local E2E Data model for queue tests ---

type E2EQueueItem struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// --- gRPC helpers ---

func setupInMemoryServer(agent *E2EAgentServer) (*grpc.Server, *bufconn.Listener) {
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	pb.RegisterMonitorServiceServer(s, agent)
	go func() {
		_ = s.Serve(lis)
	}()
	return s, lis
}

func dialInMemory(t *testing.T, lis *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// --- E2E Tests: MonitorService RPCs (table-driven) ---

func TestE2E_MonitorService_RPCs(t *testing.T) {
	agent := NewE2EAgentServer()
	_, lis := setupInMemoryServer(agent)
	conn := dialInMemory(t, lis)
	client := pb.NewMonitorServiceClient(conn)
	ctx := context.Background()

	type rpcCase struct {
		name           string
		invoke         func() error
		wantErr        bool
		wantCallsField string
		wantCallCount  int
	}

	cases := []rpcCase{
		{
			name: "GetSystemStatus",
			invoke: func() error {
				_, err := client.GetSystemStatus(ctx, &pb.GetSystemStatusRequest{})
				return err
			},
			wantCallsField: "SystemStatusCalls",
			wantCallCount:  1,
		},
		{
			name: "TestSystems",
			invoke: func() error {
				_, err := client.TestSystems(ctx, &pb.TestSystemsRequest{})
				return err
			},
			wantCallsField: "TestSystemsCalls",
			wantCallCount:  1,
		},
		{
			name: "TriggerBackup",
			invoke: func() error {
				_, err := client.TriggerBackup(ctx, &pb.TriggerBackupRequest{})
				return err
			},
			wantCallsField: "TriggerBackupCalls",
			wantCallCount:  1,
		},
		{
			name: "Reboot",
			invoke: func() error {
				_, err := client.Reboot(ctx, &pb.RebootRequest{})
				return err
			},
			wantCallsField: "RebootCalls",
			wantCallCount:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			agent.mu.Lock()
			defer agent.mu.Unlock()
			var gotCalls int
			switch tc.wantCallsField {
			case "SystemStatusCalls":
				gotCalls = agent.SystemStatusCalls
			case "TestSystemsCalls":
				gotCalls = agent.TestSystemsCalls
			case "TriggerBackupCalls":
				gotCalls = agent.TriggerBackupCalls
			case "RebootCalls":
				gotCalls = agent.RebootCalls
			}
			if gotCalls != tc.wantCallCount {
				t.Errorf("expected %d calls to %s, got %d", tc.wantCallCount, tc.wantCallsField, gotCalls)
			}
		})
	}
}

func TestE2E_GetSystemStatus_ResponseContent(t *testing.T) {
	agent := NewE2EAgentServer()
	_, lis := setupInMemoryServer(agent)
	conn := dialInMemory(t, lis)
	client := pb.NewMonitorServiceClient(conn)
	ctx := context.Background()

	res, err := client.GetSystemStatus(ctx, &pb.GetSystemStatusRequest{})
	if err != nil {
		t.Fatalf("GetSystemStatus error: %v", err)
	}

	if res.BatteryCapacity != 95 || res.BatteryStatus != "Charging" {
		t.Errorf("unexpected battery: %d%% %s", res.BatteryCapacity, res.BatteryStatus)
	}
	if res.CpuTemperature != 45.5 {
		t.Errorf("unexpected cpu temp: %f", res.CpuTemperature)
	}
	if res.ServerModel != "TestServer" {
		t.Errorf("unexpected server model: %s", res.ServerModel)
	}
	if len(res.Disks) != 1 || res.Disks[0].Name != "SSD" {
		t.Errorf("unexpected disks: %v", res.Disks)
	}
}

func TestE2E_RPC_ServerError(t *testing.T) {
	agent := NewE2EAgentServer()
	agent.SystemStatusErr = fmt.Errorf("agent internal error")
	_, lis := setupInMemoryServer(agent)
	conn := dialInMemory(t, lis)
	client := pb.NewMonitorServiceClient(conn)
	ctx := context.Background()

	_, err := client.GetSystemStatus(ctx, &pb.GetSystemStatusRequest{})
	if err == nil {
		t.Fatal("expected error from agent, got nil")
	}
	if !strings.Contains(err.Error(), "agent internal error") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- E2E Tests: StreamEvents pipeline (agent -> gRPC stream -> queue -> Telegram) ---

func TestE2E_StreamPipeline(t *testing.T) {
	agent := NewE2EAgentServer()
	_, lis := setupInMemoryServer(agent)
	conn := dialInMemory(t, lis)
	client := pb.NewMonitorServiceClient(conn)
	mockBot := &E2EMockTelegramSender{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inpChan := make(chan E2EQueueItem, 10)
	listQueue := queue.NewListQueue[E2EQueueItem]()
	outChan := queue.AddQueue(ctx, listQueue, inpChan)

	// Worker: queue -> Telegram
	go func() {
		for item := range outChan {
			msg := tgbotapi.NewMessage(item.ChatID, item.Text)
			_, _ = mockBot.Send(msg)
		}
	}()

	// Stream Listener: gRPC -> queue
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

	agent.SendEvent(&pb.StreamEventsResponse{
		Type:    "battery",
		Message: "🪫 Low battery alert!",
	})

	// Wait for delivery
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := mockBot.GetSentMessages(); len(msgs) >= 1 {
			msgConfig := msgs[0].(tgbotapi.MessageConfig)
			if msgConfig.Text != "🪫 Low battery alert!" {
				t.Errorf("Unexpected message text: %s", msgConfig.Text)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Expected 1 delivered message, got 0")
}

func TestE2E_StreamEvents_Received(t *testing.T) {
	agent := NewE2EAgentServer()
	_, lis := setupInMemoryServer(agent)
	conn := dialInMemory(t, lis)
	client := pb.NewMonitorServiceClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.StreamEvents(ctx, &pb.StreamEventsRequest{})
	if err != nil {
		t.Fatalf("StreamEvents error: %v", err)
	}

	agent.SendEvent(&pb.StreamEventsResponse{Type: "test", Message: "hello stream"})

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv error: %v", err)
	}
	if event.Type != "test" || event.Message != "hello stream" {
		t.Errorf("unexpected event: %+v", event)
	}
}

// --- E2E Tests: Queue persistence and recovery (disk) ---

func TestE2E_Queue_PersistenceAndRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	queueFilePath := filepath.Join(tmpDir, "e2e_queue.json")

	pendingMsg := E2EQueueItem{ChatID: 12345, Text: "🔥 CPU Overheat 85°C"}

	listQueue := queue.NewListQueue[E2EQueueItem]()
	listQueue.Push(E2EQueueItem{ChatID: 12345, Text: "⚠️ Disk Full"})

	exportedData, err := queue.Export(listQueue, func(items []E2EQueueItem) ([]byte, error) {
		return json.Marshal(items)
	})
	if err != nil {
		t.Fatalf("queue export failed: %v", err)
	}

	var queueItems []E2EQueueItem
	if err := json.Unmarshal(exportedData, &queueItems); err != nil {
		t.Fatalf("queue unmarshal failed: %v", err)
	}

	remaining := append([]E2EQueueItem{pendingMsg}, queueItems...)

	finalData, err := json.MarshalIndent(remaining, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(queueFilePath, finalData, 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	savedData, err := os.ReadFile(queueFilePath)
	if err != nil {
		t.Fatalf("read queue file: %v", err)
	}

	var savedItems []E2EQueueItem
	if err := json.Unmarshal(savedData, &savedItems); err != nil {
		t.Fatalf("unmarshal saved: %v", err)
	}

	if len(savedItems) != 2 {
		t.Fatalf("Expected 2 items saved, got %d", len(savedItems))
	}
	if savedItems[0].Text != "🔥 CPU Overheat 85°C" {
		t.Errorf("First item should be pending message, got: %s", savedItems[0].Text)
	}

	// Recovery
	restoredQueue := queue.NewListQueue[E2EQueueItem]()
	if err := queue.Import(restoredQueue, savedData, func(b []byte) ([]E2EQueueItem, error) {
		var items []E2EQueueItem
		err := json.Unmarshal(b, &items)
		return items, err
	}); err != nil {
		t.Fatalf("queue import failed: %v", err)
	}

	if restoredQueue.Len() != 2 {
		t.Errorf("Expected 2 restored items, got %d", restoredQueue.Len())
	}
}

func TestE2E_Queue_CorruptedFileHandling(t *testing.T) {
	tmpDir := t.TempDir()
	corruptedPath := filepath.Join(tmpDir, "corrupted_queue.json")
	if err := os.WriteFile(corruptedPath, []byte("{invalid json content..."), 0644); err != nil {
		t.Fatalf("write corrupted file: %v", err)
	}

	q := queue.NewListQueue[E2EQueueItem]()
	err := queue.Import(q, []byte("{invalid json content..."), func(b []byte) ([]E2EQueueItem, error) {
		var items []E2EQueueItem
		err := json.Unmarshal(b, &items)
		return items, err
	})
	if err == nil {
		t.Fatal("expected import error for corrupted JSON")
	}

	if q.Len() != 0 {
		t.Errorf("Expected empty queue on corrupted data, got %d", q.Len())
	}
}

// --- E2E Tests: Telegram send failure handling ---

func TestE2E_TelegramSendFailureAndRecovery(t *testing.T) {
	agent := NewE2EAgentServer()
	_, lis := setupInMemoryServer(agent)
	conn := dialInMemory(t, lis)
	client := pb.NewMonitorServiceClient(conn)
	mockBot := &E2EMockTelegramSender{}
	mockBot.SetFailNext(1) // first send fails, second succeeds

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inpChan := make(chan E2EQueueItem, 10)
	listQueue := queue.NewListQueue[E2EQueueItem]()
	outChan := queue.AddQueue(ctx, listQueue, inpChan)

	go func() {
		for item := range outChan {
			msg := tgbotapi.NewMessage(item.ChatID, item.Text)
			// Simulate the bot worker retry logic: retry on send failure
			for i := 0; i < 3; i++ {
				if _, err := mockBot.Send(msg); err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

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

	agent.SendEvent(&pb.StreamEventsResponse{Type: "battery", Message: "retry me"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := mockBot.GetSentMessages(); len(msgs) >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Expected message to be delivered after retry, got 0")
}
