package e2e

import (
	"context"
	"fmt"
	"sync"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- Mock Telegram Sender (shared between e2e tests) ---

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

func (m *E2EMockTelegramSender) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentMessages = nil
	m.failNextCount = 0
}

func (m *E2EMockTelegramSender) SetFailNext(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNextCount = n
}

// --- Mock Agent Server implementing full MonitorService ---

type E2EAgentServer struct {
	pb.UnimplementedMonitorServiceServer
	EventChan chan *pb.StreamEventsResponse

	// Configurable responses per RPC
	SystemStatusResp *pb.GetSystemStatusResponse
	SystemStatusErr  error

	TestSystemsResp *pb.TestSystemsResponse
	TestSystemsErr  error

	TriggerBackupResp *pb.TriggerBackupResponse
	TriggerBackupErr  error

	RebootResp *pb.RebootResponse
	RebootErr  error

	// Call counters
	SystemStatusCalls int
	TestSystemsCalls  int
	TriggerBackupCalls int
	RebootCalls       int

	mu sync.Mutex
}

func NewE2EAgentServer() *E2EAgentServer {
	return &E2EAgentServer{
		EventChan: make(chan *pb.StreamEventsResponse, 100),
		SystemStatusResp: &pb.GetSystemStatusResponse{
			BatteryCapacity: 95,
			BatteryStatus:   "Charging",
			CpuTemperature:  45.5,
			ServerModel:     "TestServer",
			Timestamp:       "2023-10-27T10:00:00Z",
			Disks: []*pb.DiskUsage{
				{Name: "SSD", UsedGb: 50, TotalGb: 100, UsagePct: 50},
			},
		},
		TestSystemsResp:   &pb.TestSystemsResponse{ResultMessage: "Test event successfully sent to stream."},
		TriggerBackupResp: &pb.TriggerBackupResponse{ResultMessage: "Forced storage backup started via Telegram command."},
		RebootResp:        &pb.RebootResponse{ResultMessage: "Server reboot initiated immediately."},
	}
}

func (s *E2EAgentServer) GetSystemStatus(ctx context.Context, in *pb.GetSystemStatusRequest) (*pb.GetSystemStatusResponse, error) {
	s.mu.Lock()
	s.SystemStatusCalls++
	resp := s.SystemStatusResp
	err := s.SystemStatusErr
	s.mu.Unlock()
	return resp, err
}

func (s *E2EAgentServer) StreamEvents(in *pb.StreamEventsRequest, stream pb.MonitorService_StreamEventsServer) error {
	for event := range s.EventChan {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

func (s *E2EAgentServer) TestSystems(ctx context.Context, in *pb.TestSystemsRequest) (*pb.TestSystemsResponse, error) {
	s.mu.Lock()
	s.TestSystemsCalls++
	resp := s.TestSystemsResp
	err := s.TestSystemsErr
	s.mu.Unlock()
	return resp, err
}

func (s *E2EAgentServer) TriggerBackup(ctx context.Context, in *pb.TriggerBackupRequest) (*pb.TriggerBackupResponse, error) {
	s.mu.Lock()
	s.TriggerBackupCalls++
	resp := s.TriggerBackupResp
	err := s.TriggerBackupErr
	s.mu.Unlock()
	return resp, err
}

func (s *E2EAgentServer) Reboot(ctx context.Context, in *pb.RebootRequest) (*pb.RebootResponse, error) {
	s.mu.Lock()
	s.RebootCalls++
	resp := s.RebootResp
	err := s.RebootErr
	s.mu.Unlock()
	return resp, err
}

func (s *E2EAgentServer) SendEvent(event *pb.StreamEventsResponse) {
	s.EventChan <- event
}

// Ensure E2EAgentServer implements the full grpc service interface at compile time.
var _ pb.MonitorServiceServer = (*E2EAgentServer)(nil)
