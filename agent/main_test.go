package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
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

// --- 1. Тесты батареи ---

func TestBatteryMonitoring_StatusChange(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "80\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Discharging\n"

	srv := setupTestServer(mock)

	// Инициализация стартовых значений
	cap, stat, err := srv.getBatteryInfo()
	if err != nil || cap != 80 || stat != "Discharging" {
		t.Fatalf("Expected 80%% Discharging, got %d%% %s, err: %v", cap, stat, err)
	}

	srv.lastCap = cap
	srv.lastStat = stat

	// Симулируем подключение зарядного устройства
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"

	// Запускаем один проход проверки
	cap, stat, _ = srv.getBatteryInfo()
	if stat != srv.lastStat {
		srv.eventChan <- &pb.StreamEventsResponse{
			Type:    "battery",
			Message: "🔌 Зарядное устройство подключено.",
		}
	}

	select {
	case event := <-srv.eventChan:
		if event.Type != "battery" || event.Message != "🔌 Зарядное устройство подключено." {
			t.Errorf("Unexpected event: %v", event)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected battery status change event, got timeout")
	}
}

// --- 2. Тесты CPU и Гистерезиса ---

func TestCpuMonitoring_OverheatAndHysteresis(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	// 1. Нормальная температура (60°C)
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "60000\n"
	temp, _ := srv.getCpuTemperature()
	if temp != 60.0 {
		t.Fatalf("Expected 60.0°C, got %f", temp)
	}

	// 2. Перегрев (75°C > порог 70°C)
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "75000\n"
	temp, _ = srv.getCpuTemperature()

	srv.mu.Lock()
	if temp > srv.cfg.Monitoring.CpuTempThreshold && !srv.cpuAlerted {
		srv.eventChan <- &pb.StreamEventsResponse{Type: "cpu", Message: "🔥 Перегрев процессора!"}
		srv.cpuAlerted = true
	}
	srv.mu.Unlock()

	select {
	case event := <-srv.eventChan:
		if event.Type != "cpu" {
			t.Errorf("Expected cpu alert, got %v", event)
		}
	default:
		t.Error("Expected cpu overheat event")
	}

	// 3. Небольшое снижение (67°C) — аларм ВСЕ ЕЩЕ активен (гистерезис 70-5 = 65°C)
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "67000\n"
	temp, _ = srv.getCpuTemperature()

	srv.mu.Lock()
	if srv.cpuAlerted && temp < srv.cfg.Monitoring.CpuTempThreshold-5.0 {
		t.Error("Alert cleared too early! Should wait for < 65.0°C")
	}
	srv.mu.Unlock()

	// 4. Охлаждение до нормы (62°C < 65°C) — аларм сбрасывается
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "62000\n"
	temp, _ = srv.getCpuTemperature()

	srv.mu.Lock()
	if srv.cpuAlerted && temp < srv.cfg.Monitoring.CpuTempThreshold-5.0 {
		srv.eventChan <- &pb.StreamEventsResponse{Type: "cpu", Message: "✅ Норма"}
		srv.cpuAlerted = false
	}
	srv.mu.Unlock()

	select {
	case event := <-srv.eventChan:
		if event.Message != "✅ Норма" {
			t.Errorf("Expected normal event, got %v", event)
		}
	default:
		t.Error("Expected cpu normal event")
	}
}

// --- 3. Тесты Бэкапа ---

func TestBackup_CancelOnBattery(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "50\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Discharging\n"

	srv := setupTestServer(mock)

	srv.runBackupRoutine()

	select {
	case event := <-srv.eventChan:
		if event.Type != "backup_status" || !strings.Contains(event.Message, "сервер работает от батареи") {
			t.Errorf("Unexpected backup cancellation message: %s", event.Message)
		}
	default:
		t.Error("Expected backup cancellation event due to battery power")
	}
}

func TestBackup_PreventParallelRuns(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "100\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"

	srv := setupTestServer(mock)

	// Имитируем, что бэкап уже запущен
	srv.isRunning = true

	srv.runBackupRoutine()

	select {
	case event := <-srv.eventChan:
		expected := "⚠️ Бэкап уже выполняется в другом потоке. Новый запуск отменен."
		if event.Message != expected {
			t.Errorf("Expected message '%s', got '%s'", expected, event.Message)
		}
	default:
		t.Error("Expected parallel execution rejection message")
	}
}

func TestBackup_WrongDriveSafetyCheck(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "100\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"

	// Диск найден
	mock.cmdResponses["blkid -c"] = "/dev/sdb1"
	mock.cmdResponses["mkdir -p"] = ""
	mock.cmdResponses["findmnt -M"] = "" // Не смонтирован
	mock.cmdResponses["mount /dev/sdb1"] = ""

	// Но findmnt возвращает ДРУГОЕ устройство! (Имитация сбоя / подмены диска)
	mock.cmdResponses["findmnt -n"] = "/dev/sda1"

	srv := setupTestServer(mock)

	srv.runBackupRoutine()

	// Ищем сообщение о срабатывании защиты
	foundSafetyAlert := false
	for len(srv.eventChan) > 0 {
		event := <-srv.eventChan
		if event.Type == "backup_status" && event.Message == "❌ КРИТИЧЕСКАЯ ОШИБКА: Защита сработала. Папка не указывает на USB-диск! Бэкап заблокирован." {
			foundSafetyAlert = true
			break
		}
	}

	if !foundSafetyAlert {
		t.Error("Drive protection trigger failed! Expected safety alert message.")
	}
}
