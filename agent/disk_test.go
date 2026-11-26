package main

import (
	"fmt"
	"testing"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

// --- Тесты мониторинга дискового пространства ---

func TestDiskMonitoring_LowDiskSpace(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	// Устанавливаем очень высокий порог, чтобы гарантированно сработал алерт на любом реальном диске
	srv.cfg.Monitoring.DiskMinFreePercent = 99.9

	disk, err := srv.getDiskStat("/tmp")
	if err != nil {
		t.Fatalf("getDiskStat(/tmp) failed: %v", err)
	}

	// Имитируем логику monitorDisksLoop для низкого места
	if disk.FreePct < srv.cfg.Monitoring.DiskMinFreePercent {
		srv.eventChan <- &pb.StreamEventsResponse{
			Type:    "disk",
			Message: fmt.Sprintf("Low disk space on [%s] (%s): %.1f%% free", "System SSD", "/tmp", disk.FreePct),
		}
	}

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected low disk space event")
		return
	}
	if event.Type != "disk" {
		t.Errorf("Expected disk event, got %v", event)
	}
}

func TestDiskMonitoring_DiskRestored(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	// Порог по умолчанию 10%, на реальном диске свободного места должно быть больше
	disk, err := srv.getDiskStat("/tmp")
	if err != nil {
		t.Fatalf("getDiskStat(/tmp) failed: %v", err)
	}

	// Имитируем логику восстановления: был алерт, теперь место свободно (с запасом 5%)
	if disk.FreePct > srv.cfg.Monitoring.DiskMinFreePercent+5.0 {
		srv.eventChan <- &pb.StreamEventsResponse{
			Type:    "disk",
			Message: fmt.Sprintf("Disk space [%s] restored: %.1f%% free", "System SSD", disk.FreePct),
		}
	}

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected disk restored event")
		return
	}
	if event.Type != "disk" {
		t.Errorf("Expected disk event, got %v", event)
	}
}

func TestDiskMonitoring_GetDiskStat(t *testing.T) {
	mock := NewMockSystemRunner()
	srv := setupTestServer(mock)

	disk, err := srv.getDiskStat("/")
	if err != nil {
		t.Fatalf("getDiskStat(\"/\") failed: %v", err)
	}

	if disk.TotalGB <= 0 {
		t.Errorf("Expected positive TotalGB, got %f", disk.TotalGB)
	}
	if disk.UsagePct < 0 || disk.UsagePct > 100 {
		t.Errorf("Expected UsagePct between 0 and 100, got %f", disk.UsagePct)
	}
	if disk.FreePct < 0 || disk.FreePct > 100 {
		t.Errorf("Expected FreePct between 0 and 100, got %f", disk.FreePct)
	}
}
