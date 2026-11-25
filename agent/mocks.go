package main

import (
	"context"
	"errors"
	"sync"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

// MockSystemRunner симулирует системные вызовы и чтение файлов
type MockSystemRunner struct {
	mu           sync.Mutex
	files        map[string]string
	cmdResponses map[string]string
	cmdErrors    map[string]error
	executedCmds []string
}

func NewMockSystemRunner() *MockSystemRunner {
	return &MockSystemRunner{
		files:        make(map[string]string),
		cmdResponses: make(map[string]string),
		cmdErrors:    make(map[string]error),
	}
}

func (m *MockSystemRunner) ReadFile(filename string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	content, ok := m.files[filename]
	if !ok {
		return nil, errors.New("file not found")
	}
	return []byte(content), nil
}

func (m *MockSystemRunner) RunCmdWithContext(ctx context.Context, name string, arg ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmdKey := name
	if len(arg) > 0 {
		cmdKey = name + " " + arg[0]
	}
	m.executedCmds = append(m.executedCmds, cmdKey)

	if err, ok := m.cmdErrors[cmdKey]; ok && err != nil {
		return m.cmdResponses[cmdKey], err
	}
	return m.cmdResponses[cmdKey], nil
}

// setupTestServer создаёт сервер с тестовой конфигурацией и переданным моком.
func setupTestServer(mock *MockSystemRunner) *server {
	cfg := &config{}
	cfg.Monitoring.BatteryIntervalMin = 1
	cfg.Monitoring.CpuIntervalMin = 1
	cfg.Monitoring.CpuTempThreshold = 70.0
	cfg.Monitoring.DiskIntervalMin = 1
	cfg.Monitoring.DiskMinFreePercent = 10.0

	cfg.Backup.UUID = "test-uuid-1234"
	cfg.Backup.SourceDir = "/tmp/source"
	cfg.Backup.MountPoint = "/tmp/target"
	cfg.Backup.XhciPCI = "0000:00:14.0"
	cfg.Backup.ScheduleTime = "03:00"

	return &server{
		cfg:         cfg,
		eventChan:   make(chan *pb.StreamEventsResponse, 100),
		diskAlerted: make(map[string]bool),
		sysRunner:   mock,
	}
}

// waitForEvent ожидает событие из eventChan в течение 100 мс.
func waitForEvent(srv *server) (*pb.StreamEventsResponse, bool) {
	select {
	case event := <-srv.eventChan:
		return event, true
	case <-time.After(100 * time.Millisecond):
		return nil, false
	}
}
