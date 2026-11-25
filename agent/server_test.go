package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

// --- Тесты gRPC методов ---

func TestGrpc_GetSystemStatus(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "80\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "60000\n"

	srv := setupTestServer(mock)

	resp, err := srv.GetSystemStatus(context.Background(), &pb.GetSystemStatusRequest{})
	if err != nil {
		t.Fatalf("GetSystemStatus failed: %v", err)
	}

	if resp.BatteryCapacity != 80 {
		t.Errorf("Expected BatteryCapacity 80, got %d", resp.BatteryCapacity)
	}
	if resp.BatteryStatus != "Charging" {
		t.Errorf("Expected BatteryStatus Charging, got %s", resp.BatteryStatus)
	}
	if resp.CpuTemperature != 60.0 {
		t.Errorf("Expected CpuTemperature 60.0, got %f", resp.CpuTemperature)
	}
	if resp.ServerModel == "" {
		t.Error("Expected non-empty ServerModel")
	}
	if resp.Timestamp == "" {
		t.Error("Expected non-empty Timestamp")
	}
}

func TestGrpc_StreamEvents(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	// Создаём тестовый стрим
	stream := &testEventStream{received: make(chan *pb.StreamEventsResponse, 10)}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.StreamEvents(&pb.StreamEventsRequest{}, stream)
	}()

	// Отправляем тестовое событие в канал сервера
	srv.eventChan <- &pb.StreamEventsResponse{
		Type:    "test",
		Message: "Test event for StreamEvents",
	}

	// Ждём получения события клиентом
	select {
	case event := <-stream.received:
		if event.Type != "test" || event.Message != "Test event for StreamEvents" {
			t.Errorf("Unexpected stream event: %v", event)
		}
	case err := <-errCh:
		t.Fatalf("StreamEvents returned error: %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Error("Timeout waiting for stream event")
	}

	// Закрываем канал сервера, чтобы StreamEvents завершился
	close(srv.eventChan)
	<-errCh
}

func TestGrpc_TestSystems(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	resp, err := srv.TestSystems(context.Background(), &pb.TestSystemsRequest{})
	if err != nil {
		t.Fatalf("TestSystems failed: %v", err)
	}

	if resp.ResultMessage != "Test event successfully sent to stream." {
		t.Errorf("Unexpected ResultMessage: %s", resp.ResultMessage)
	}

	// Проверяем, что событие попало в eventChan
	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected test event in eventChan")
		return
	}
	if event.Type != "test" {
		t.Errorf("Expected type 'test', got %s", event.Type)
	}
}

func TestGrpc_TriggerBackup_OnBattery(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "50\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Discharging\n"

	srv := setupTestServer(mock)

	resp, err := srv.TriggerBackup(context.Background(), &pb.TriggerBackupRequest{})
	if err != nil {
		t.Fatalf("TriggerBackup failed: %v", err)
	}

	expectedMsg := "Backup cancelled: server running on battery (50%, Discharging). Backup only runs on AC power."
	if resp.ResultMessage != expectedMsg {
		t.Errorf("Unexpected ResultMessage:\n  expected: %s\n  got:      %s", expectedMsg, resp.ResultMessage)
	}
}

func TestGrpc_TriggerBackup_OnAcPower(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "100\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"
	mock.cmdResponses["blkid -c"] = "/dev/sdb1"
	mock.cmdResponses["mkdir -p"] = ""
	mock.cmdResponses["findmnt -M"] = ""
	mock.cmdResponses["mount /dev/sdb1"] = ""
	mock.cmdResponses["findmnt -n"] = "/dev/sdb1"
	mock.cmdResponses["rsync -aHAX"] = ""
	mock.cmdResponses["umount -l"] = ""
	mock.cmdResponses["udisksctl power-off"] = ""

	srv := setupTestServer(mock)

	resp, err := srv.TriggerBackup(context.Background(), &pb.TriggerBackupRequest{})
	if err != nil {
		t.Fatalf("TriggerBackup failed: %v", err)
	}

	if resp.ResultMessage != "Forced storage backup started via Telegram command." {
		t.Errorf("Unexpected ResultMessage: %s", resp.ResultMessage)
	}
}

func TestGrpc_Reboot_WhenBackupRunning(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	srv.isRunning = true

	resp, err := srv.Reboot(context.Background(), &pb.RebootRequest{})
	if err != nil {
		t.Fatalf("Reboot failed: %v", err)
	}

	if resp.ResultMessage != "Reboot cancelled: backup is currently running. Please try again later." {
		t.Errorf("Unexpected ResultMessage: %s", resp.ResultMessage)
	}
}

func TestGrpc_Reboot_WhenIdle(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	srv.isRunning = false

	resp, err := srv.Reboot(context.Background(), &pb.RebootRequest{})
	if err != nil {
		t.Fatalf("Reboot failed: %v", err)
	}

	if resp.ResultMessage != "Server reboot initiated immediately." {
		t.Errorf("Unexpected ResultMessage: %s", resp.ResultMessage)
	}
}

// --- Тестовый стрим для gRPC StreamEvents ---

type testEventStream struct {
	received chan *pb.StreamEventsResponse
}

func (s *testEventStream) Send(event *pb.StreamEventsResponse) error {
	s.received <- event
	return nil
}

func (s *testEventStream) SetHeader(metadata.MD) error { return nil }
func (s *testEventStream) SendHeader(metadata.MD) error { return nil }
func (s *testEventStream) SetTrailer(metadata.MD) {}
func (s *testEventStream) Context() context.Context                    { return context.Background() }
func (s *testEventStream) Stream() pb.MonitorService_StreamEventsServer {
	return s
}
func (s *testEventStream) RecvMsg(msg interface{}) error { return nil }
func (s *testEventStream) SendMsg(msg interface{}) error { return nil }

var _ grpc.ServerStream = (*testEventStream)(nil)
