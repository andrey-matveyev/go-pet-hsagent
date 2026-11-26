package main

import (
	"strings"
	"testing"
)

// --- Тесты бэкапа ---

func TestBackup_CancelOnBattery(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "50\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Discharging\n"

	srv := setupTestServer(mock)

	srv.runBackupRoutine()

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected backup cancellation event due to battery power")
		return
	}
	if event.Type != "backup_status" || !strings.Contains(event.Message, "Backup cancelled: server running on battery") {
		t.Errorf("Unexpected backup cancellation message: %s", event.Message)
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

	event, ok := waitForEvent(srv)
	if !ok {
		t.Error("Expected parallel execution rejection message")
		return
	}
	expected := "Backup already running in another thread. New launch cancelled."
	if event.Message != expected {
		t.Errorf("Expected message '%s', got '%s'", expected, event.Message)
	}
}

func TestBackup_WrongDriveSafetyCheck(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "100\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"

	// Диск найден (ключ: "blkid -c", так как RunCmdWithContext использует только первый аргумент)
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
		if event.Type == "backup_status" && event.Message == "Critical error: Protection triggered. Folder does not point to USB drive! Backup blocked." {
			foundSafetyAlert = true
			break
		}
	}

	if !foundSafetyAlert {
		t.Error("Drive protection trigger failed! Expected safety alert message.")
	}
}

// --- Тест перезапуска USB порта ---

func TestBackup_USBRestart_DiskNotFound(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "100\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"

	// blkid не находит диск (пустой результат)
	mock.cmdResponses["blkid -c"] = ""
	// setXHCIBind (unbind и bind) проходят успешно
	mock.cmdResponses["sh -c"] = ""

	srv := setupTestServer(mock)

	srv.runBackupRoutine()

	// Ожидаем ошибку: диск не найден даже после перезапуска USB
	foundError := false
	for len(srv.eventChan) > 0 {
		event := <-srv.eventChan
		if event.Type == "backup_status" && event.Message == "Backup error: External hard drive not found! Check USB cable." {
			foundError = true
			break
		}
	}

	if !foundError {
		t.Error("Expected 'External hard drive not found' error after USB restart")
	}
}

// --- Тест ошибки rsync ---

func TestBackup_RsyncError(t *testing.T) {
	mock := NewMockSystemRunner()
	mock.files["/sys/class/power_supply/BAT0/capacity"] = "100\n"
	mock.files["/sys/class/power_supply/BAT0/status"] = "Charging\n"

	// blkid находит диск
	mock.cmdResponses["blkid -c"] = "/dev/sdb1"
	mock.cmdResponses["mkdir -p"] = ""
	mock.cmdResponses["findmnt -M"] = ""
	mock.cmdResponses["mount /dev/sdb1"] = ""
	mock.cmdResponses["findmnt -n"] = "/dev/sdb1"
	// rsync возвращает ошибку
	mock.cmdResponses["rsync -aHAX"] = "rsync error"
	mock.cmdErrors["rsync -aHAX"] = &rsyncError{}
	mock.cmdResponses["umount -l"] = ""
	mock.cmdResponses["udisksctl power-off"] = ""

	srv := setupTestServer(mock)

	srv.runBackupRoutine()

	// Ожидаем сообщение об ошибке rsync
	foundRsyncError := false
	for len(srv.eventChan) > 0 {
		event := <-srv.eventChan
		if event.Type == "backup_status" && strings.Contains(event.Message, "Error executing rsync") {
			foundRsyncError = true
			break
		}
	}

	if !foundRsyncError {
		t.Error("Expected rsync error message")
	}
}

// rsyncError реализует error для симуляции ошибки rsync
type rsyncError struct{}

func (e *rsyncError) Error() string { return "rsync failed" }

