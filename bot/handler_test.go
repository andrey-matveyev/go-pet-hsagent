package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/andrey-matveyev/go-library-queue/queue"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"google.golang.org/grpc"
)

// --- Mock TelegramSender ---

type MockTelegramSender struct {
	mu            sync.Mutex
	sentMessages  []tgbotapi.Chattable
	failNextCount int
}

func (m *MockTelegramSender) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextCount > 0 {
		m.failNextCount--
		return tgbotapi.Message{}, fmt.Errorf("network error: connection refused")
	}

	m.sentMessages = append(m.sentMessages, c)
	return tgbotapi.Message{MessageID: len(m.sentMessages)}, nil
}

func (m *MockTelegramSender) GetSentMessages() []tgbotapi.Chattable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]tgbotapi.Chattable{}, m.sentMessages...)
}

// --- Mock gRPC Client ---

type MockMonitorClient struct {
	pb.MonitorServiceClient
}

func (m *MockMonitorClient) GetBatteryStatus(ctx context.Context, in *pb.GetBatteryStatusRequest, opts ...grpc.CallOption) (*pb.GetBatteryStatusResponse, error) {
	return &pb.GetBatteryStatusResponse{
		BatteryCapacity: 95,
		BatteryStatus:   "Charging",
		CpuTemperature:  45.5,
		DiskUsageInfo:   "💻 System SSD: 20GB free",
	}, nil
}

func (m *MockMonitorClient) TestSystems(ctx context.Context, in *pb.TestSystemsRequest, opts ...grpc.CallOption) (*pb.TestSystemsResponse, error) {
	return &pb.TestSystemsResponse{ResultMessage: "Test Passed!"}, nil
}

func (m *MockMonitorClient) TriggerBackup(ctx context.Context, in *pb.TriggerBackupRequest, opts ...grpc.CallOption) (*pb.TriggerBackupResponse, error) {
	return &pb.TriggerBackupResponse{ResultMessage: "Backup Triggered!"}, nil
}

// --- 1. Тесты загрузки и сохранения очереди ---

func TestQueue_LoadAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := queueFilePath
	queueFilePath = filepath.Join(tmpDir, "test_queue.json")
	defer func() { queueFilePath = originalPath }()

	// Подготавливаем тестовые данные
	items := []QueueItem{
		{ChatID: 12345, Text: "Message 1"},
		{ChatID: 12345, Text: "Message 2"},
	}
	data, _ := json.Marshal(items)
	_ = os.WriteFile(queueFilePath, data, 0644)

	// Читаем в очередь
	q := queue.NewListQueue[QueueItem]()
	loadQueueFromFile(q)

	if q.Len() != 2 {
		t.Fatalf("Expected 2 items loaded from queue, got %d", q.Len())
	}

	// Файл должен быть удален после успешного чтения
	if _, err := os.Stat(queueFilePath); !os.IsNotExist(err) {
		t.Error("Expected queue file to be deleted after loading")
	}

	// Сохраняем с активным (застрявшим) сообщением pendingMsg
	pending := &QueueItem{ChatID: 12345, Text: "Pending Message"}
	saveQueueToFile(q, pending)

	// Проверяем сохраненный файл
	savedData, err := os.ReadFile(queueFilePath)
	if err != nil {
		t.Fatalf("Failed to read saved queue file: %v", err)
	}

	var savedItems []QueueItem
	_ = json.Unmarshal(savedData, &savedItems)

	if len(savedItems) != 3 {
		t.Fatalf("Expected 3 items saved (1 pending + 2 in queue), got %d", len(savedItems))
	}

	if savedItems[0].Text != "Pending Message" {
		t.Errorf("Expected pending message to be first, got '%s'", savedItems[0].Text)
	}
}

func TestQueue_CorruptedFileHandling(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := queueFilePath
	queueFilePath = filepath.Join(tmpDir, "corrupted_queue.json")
	defer func() { queueFilePath = originalPath }()

	// Записываем поврежденный JSON
	_ = os.WriteFile(queueFilePath, []byte("{invalid json content..."), 0644)

	q := queue.NewListQueue[QueueItem]()
	loadQueueFromFile(q)

	// Очередь должна остаться пустой
	if q.Len() != 0 {
		t.Errorf("Expected empty queue on corrupted file, got %d", q.Len())
	}

	// Исходный файл должен быть переименован
	files, _ := os.ReadDir(tmpDir)
	foundCorrupted := false
	for _, f := range files {
		if strings.Contains(f.Name(), ".corrupted_") {
			foundCorrupted = true
			break
		}
	}

	if !foundCorrupted {
		t.Error("Expected corrupted file to be renamed with .corrupted_ suffix")
	}
}

// --- 2. Тесты обработки команд ---

func TestHandleCommand_Status(t *testing.T) {
	mockBot := &MockTelegramSender{}
	mockClient := &MockMonitorClient{}

	msg := &tgbotapi.Message{
		Text: "/status",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	handleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig, ok := sent[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("Expected tgbotapi.MessageConfig type")
	}

	if !strings.Contains(msgConfig.Text, "95%") || !strings.Contains(msgConfig.Text, "45.5°C") {
		t.Errorf("Command output missing expected agent metrics: %s", msgConfig.Text)
	}
}

func TestHandleCommand_Backup(t *testing.T) {
	mockBot := &MockTelegramSender{}
	mockClient := &MockMonitorClient{}

	msg := &tgbotapi.Message{
		Text: "/backup",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	handleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "Backup Triggered!") {
		t.Errorf("Unexpected backup command response: %s", msgConfig.Text)
	}
}
