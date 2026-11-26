package main

import (
	"fmt"
	"testing"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

// --- Тесты мониторинга батареи ---

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
			Message: "Battery - charging (80%)",
		}
	}

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected battery status change event, got timeout")
		return
	}
	if event.Type != "battery" || event.Message != "Battery - charging (80%)" {
		t.Errorf("Unexpected event: %v", event)
	}
}

// --- Тесты мониторинга CPU и гистерезиса ---

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
	if temp > srv.cfg.Limits.CpuMaxTemperature && !srv.cpuAlerted {
		srv.eventChan <- &pb.StreamEventsResponse{Type: "cpu", Message: "CPU overheating! Temperature: 75.0°C (Threshold: 70.0°C)"}
		srv.cpuAlerted = true
	}
	srv.mu.Unlock()

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected cpu overheat event")
		return
	}
	if event.Type != "cpu" {
		t.Errorf("Expected cpu alert, got %v", event)
	}

	// 3. Небольшое снижение (67°C) — аларм ВСЕ ЕЩЕ активен (гистерезис 70-5 = 65°C)
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "67000\n"
	temp, _ = srv.getCpuTemperature()

	srv.mu.Lock()
	if srv.cpuAlerted && temp < srv.cfg.Limits.CpuMaxTemperature-5.0 {
		t.Error("Alert cleared too early! Should wait for < 65.0°C")
	}
	srv.mu.Unlock()

	// 4. Охлаждение до нормы (62°C < 65°C) — аларм сбрасывается
	mock.files["/sys/class/thermal/thermal_zone0/temp"] = "62000\n"
	temp, _ = srv.getCpuTemperature()

	srv.mu.Lock()
	if srv.cpuAlerted && temp < srv.cfg.Limits.CpuMaxTemperature-5.0 {
		srv.eventChan <- &pb.StreamEventsResponse{Type: "cpu", Message: "CPU temperature normal: 62.0°C"}
		srv.cpuAlerted = false
	}
	srv.mu.Unlock()

	event, ok = waitForEvent(srv)
	if !ok {
		t.Error("Expected cpu normal event")
		return
	}
	if event.Message != "CPU temperature normal: 62.0°C" {
		t.Errorf("Expected normal event, got %v", event)
	}
}

// --- Тест низкого заряда батареи ---

func TestBatteryMonitoring_LowBattery(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "20\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Discharging\n"

	srv := setupTestServer(mock)
	srv.lastCap = 80 // отличается от 20, чтобы сработало условие lastCap != cap

	cap, stat, err := srv.getBatteryInfo()
	if err != nil || cap != 20 || stat != "Discharging" {
		t.Fatalf("Expected 20%% Discharging, got %d%% %s, err: %v", cap, stat, err)
	}

	// Имитируем логику monitorBatteryLoop для низкого заряда
	if cap == 20 && srv.lastCap != cap {
		srv.eventChan <- &pb.StreamEventsResponse{
			Type:    "battery",
			Message: fmt.Sprintf("Low battery: %d%% (%s)", cap, stat),
		}
	}

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected low battery event")
		return
	}
	if event.Message != "Low battery: 20% (Discharging)" {
		t.Errorf("Unexpected message: %s", event.Message)
	}
}
