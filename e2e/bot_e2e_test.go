package e2e

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"

	pb "go-pet-hsagent/proto/hsagent/v1"

	botlib "go-pet-hsagent/botlib"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"google.golang.org/grpc"
)

// --- Mock MonitorService Client for bot command tests ---

type E2EMockMonitorClient struct {
	pb.MonitorServiceClient

	SystemStatusResp   *pb.GetSystemStatusResponse
	SystemStatusErr    error
	SystemStatusCalls  int

	TestSystemsResp   *pb.TestSystemsResponse
	TestSystemsErr    error
	TestSystemsCalls  int

	TriggerBackupResp   *pb.TriggerBackupResponse
	TriggerBackupErr    error
	TriggerBackupCalls  int

	RebootResp   *pb.RebootResponse
	RebootErr    error
	RebootCalls  int

	mu sync.Mutex
}

func (m *E2EMockMonitorClient) GetSystemStatus(ctx context.Context, in *pb.GetSystemStatusRequest, opts ...grpc.CallOption) (*pb.GetSystemStatusResponse, error) {
	m.mu.Lock()
	m.SystemStatusCalls++
	resp := m.SystemStatusResp
	err := m.SystemStatusErr
	m.mu.Unlock()
	return resp, err
}

func (m *E2EMockMonitorClient) TestSystems(ctx context.Context, in *pb.TestSystemsRequest, opts ...grpc.CallOption) (*pb.TestSystemsResponse, error) {
	m.mu.Lock()
	m.TestSystemsCalls++
	resp := m.TestSystemsResp
	err := m.TestSystemsErr
	m.mu.Unlock()
	return resp, err
}

func (m *E2EMockMonitorClient) TriggerBackup(ctx context.Context, in *pb.TriggerBackupRequest, opts ...grpc.CallOption) (*pb.TriggerBackupResponse, error) {
	m.mu.Lock()
	m.TriggerBackupCalls++
	resp := m.TriggerBackupResp
	err := m.TriggerBackupErr
	m.mu.Unlock()
	return resp, err
}

func (m *E2EMockMonitorClient) Reboot(ctx context.Context, in *pb.RebootRequest, opts ...grpc.CallOption) (*pb.RebootResponse, error) {
	m.mu.Lock()
	m.RebootCalls++
	resp := m.RebootResp
	err := m.RebootErr
	m.mu.Unlock()
	return resp, err
}

func (m *E2EMockMonitorClient) callCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch method {
	case "GetSystemStatus":
		return m.SystemStatusCalls
	case "TestSystems":
		return m.TestSystemsCalls
	case "TriggerBackup":
		return m.TriggerBackupCalls
	case "Reboot":
		return m.RebootCalls
	}
	return 0
}

// capturedLog allows asserting on log output produced by the bot package.
type capturedLog struct {
	buf *bytes.Buffer
	old interface{ Write([]byte) (int, error) }
}

func captureLogs() *capturedLog {
	buf := &bytes.Buffer{}
	old := log.Writer()
	log.SetOutput(buf)
	return &capturedLog{buf: buf, old: old}
}

func (c *capturedLog) release() string {
	log.SetOutput(c.old)
	return c.buf.String()
}

func (c *capturedLog) contains(substr string) bool {
	return strings.Contains(c.buf.String(), substr)
}

// --- Tests: HandleCommand ---

func TestE2E_HandleCommand_Status(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{
		SystemStatusResp: &pb.GetSystemStatusResponse{
			BatteryCapacity: 80,
			BatteryStatus:   "Charging",
			CpuTemperature:  55.0,
			ServerModel:     "TestServer",
			Timestamp:       "2024-01-01T00:00:00Z",
			Disks: []*pb.DiskUsage{
				{Name: "SSD", UsedGb: 30, TotalGb: 100, UsagePct: 30},
			},
		},
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/status",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig, ok := sent[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("Expected tgbotapi.MessageConfig, got %T", sent[0])
	}

	text := msgConfig.Text
	if !strings.Contains(text, "80%") {
		t.Errorf("Expected battery 80%%, got: %s", text)
	}
	if !strings.Contains(text, "55.0°C") {
		t.Errorf("Expected CPU temp 55.0°C, got: %s", text)
	}
	if !strings.Contains(text, "TestServer") {
		t.Errorf("Expected server model TestServer, got: %s", text)
	}

	if mockClient.callCount("GetSystemStatus") != 1 {
		t.Errorf("Expected 1 GetSystemStatus call, got %d", mockClient.callCount("GetSystemStatus"))
	}
}

func TestE2E_HandleCommand_Test(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{
		TestSystemsResp: &pb.TestSystemsResponse{ResultMessage: "All systems nominal"},
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/test",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 5},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "All systems nominal") {
		t.Errorf("Expected test result in response, got: %s", msgConfig.Text)
	}

	if mockClient.callCount("TestSystems") != 1 {
		t.Errorf("Expected 1 TestSystems call, got %d", mockClient.callCount("TestSystems"))
	}
}

func TestE2E_HandleCommand_Backup(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{
		TriggerBackupResp: &pb.TriggerBackupResponse{ResultMessage: "Backup started"},
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/backup",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "Backup started") {
		t.Errorf("Expected backup result in response, got: %s", msgConfig.Text)
	}

	if mockClient.callCount("TriggerBackup") != 1 {
		t.Errorf("Expected 1 TriggerBackup call, got %d", mockClient.callCount("TriggerBackup"))
	}
}

func TestE2E_HandleCommand_Reboot(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/reboot",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message (reboot confirmation), got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "Are you sure you want to reboot") {
		t.Errorf("Expected reboot confirmation prompt, got: %s", msgConfig.Text)
	}

	if msgConfig.ReplyMarkup == nil {
		t.Error("Expected inline keyboard with Yes/No buttons")
	}

	if mockClient.callCount("Reboot") != 0 {
		t.Errorf("Expected 0 Reboot calls before confirmation, got %d", mockClient.callCount("Reboot"))
	}
}

func TestE2E_HandleCommand_Help(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/help",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 5},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "/status") {
		t.Errorf("Expected /status in help, got: %s", msgConfig.Text)
	}
	if !strings.Contains(msgConfig.Text, "/test") {
		t.Errorf("Expected /test in help, got: %s", msgConfig.Text)
	}
	if !strings.Contains(msgConfig.Text, "/backup") {
		t.Errorf("Expected /backup in help, got: %s", msgConfig.Text)
	}
	if !strings.Contains(msgConfig.Text, "/reboot") {
		t.Errorf("Expected /reboot in help, got: %s", msgConfig.Text)
	}
}

func TestE2E_HandleCommand_Unknown(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/foobar",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "Unknown command") {
		t.Errorf("Expected unknown command hint, got: %s", msgConfig.Text)
	}
}

func TestE2E_HandleCommand_Status_GRPCError(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{
		SystemStatusErr: grpcErr("agent unavailable"),
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 12345},
		Text: "/status",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 7},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	sent := mockBot.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 error response message, got %d", len(sent))
	}

	msgConfig := sent[0].(tgbotapi.MessageConfig)
	if !strings.Contains(msgConfig.Text, "Failed to fetch data from Agent") {
		t.Errorf("Expected agent error hint, got: %s", msgConfig.Text)
	}
}

// --- Tests: HandleCallbackQuery (reboot confirm / cancel) ---

func TestE2E_HandleCallbackQuery_RebootConfirm(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{
		RebootResp: &pb.RebootResponse{ResultMessage: "Server reboot initiated"},
	}

	cq := &tgbotapi.CallbackQuery{
		ID:   "cb-123",
		Data: "reboot_confirm",
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 12345},
		},
	}

	botlib.HandleCallbackQuery(context.Background(), mockClient, mockBot, cq)

	if mockClient.callCount("Reboot") != 1 {
		t.Errorf("Expected 1 Reboot call on confirm, got %d", mockClient.callCount("Reboot"))
	}

	sent := mockBot.GetSentMessages()
	if len(sent) < 1 {
		t.Fatalf("Expected at least 1 message on confirm, got %d", len(sent))
	}

	lastMsg := sent[len(sent)-1].(tgbotapi.MessageConfig)
	if !strings.Contains(lastMsg.Text, "reboot initiated") {
		t.Errorf("Expected reboot confirmation message, got: %s", lastMsg.Text)
	}
}

func TestE2E_HandleCallbackQuery_RebootCancel(t *testing.T) {
	botlib.ChatID = 12345

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{}

	cq := &tgbotapi.CallbackQuery{
		ID:   "cb-456",
		Data: "reboot_cancel",
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 12345},
		},
	}

	botlib.HandleCallbackQuery(context.Background(), mockClient, mockBot, cq)

	if mockClient.callCount("Reboot") != 0 {
		t.Errorf("Expected 0 Reboot calls on cancel, got %d", mockClient.callCount("Reboot"))
	}

	sent := mockBot.GetSentMessages()
	if len(sent) < 1 {
		t.Fatalf("Expected at least 1 message on cancel, got %d", len(sent))
	}

	lastMsg := sent[len(sent)-1].(tgbotapi.MessageConfig)
	if !strings.Contains(lastMsg.Text, "Reboot cancelled") {
		t.Errorf("Expected reboot cancellation message, got: %s", lastMsg.Text)
	}
}

// --- Tests: Unauthorized chatID ---

func TestE2E_HandleCommand_UnauthorizedChatID(t *testing.T) {
	botlib.ChatID = 12345 // authorized chat

	mockBot := &E2EMockTelegramSender{}
	mockClient := &E2EMockMonitorClient{}

	captured := captureLogs()

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 99999}, // unauthorized chat
		Text: "/status",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 6},
		},
	}

	botlib.HandleCommand(context.Background(), mockClient, mockBot, msg)

	// Must log the unauthorized attempt
	logOutput := captured.release()
	if !strings.Contains(logOutput, "Unauthorized access attempt") {
		t.Errorf("Expected log about unauthorized access, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "ChatID: 99999") {
		t.Errorf("Expected log to mention ChatID 99999, got: %s", logOutput)
	}

	// No message should be sent
	sent := mockBot.GetSentMessages()
	if len(sent) != 0 {
		t.Errorf("Expected 0 messages for unauthorized chat, got %d", len(sent))
	}

	// No RPC should be called
	if mockClient.callCount("GetSystemStatus") != 0 {
		t.Errorf("Expected 0 RPC calls for unauthorized chat, got %d", mockClient.callCount("GetSystemStatus"))
	}
}

// --- Helper: grpc error ---

func grpcErr(msg string) error {
	return grpcErrImpl{msg: msg}
}

type grpcErrImpl struct {
	msg string
}

func (e grpcErrImpl) Error() string { return e.msg }

func (e grpcErrImpl) GRPCStatus() interface{} {
	return nil
}
