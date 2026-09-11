package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

func (s *server) startSchedulerLoop() {
	for {
		now := time.Now()
		if strings.EqualFold(now.Weekday().String(), s.cfg.Backup.ScheduleWeekday) && now.Format("15:04") == s.cfg.Backup.ScheduleTime {
			s.runBackupRoutine()
			time.Sleep(2 * time.Minute)
		}
		time.Sleep(30 * time.Second)
	}
}

func (s *server) runBackupRoutine() {
	if cap, stat, err := s.getBatteryInfo(); err == nil && stat == "Discharging" {
		msg := fmt.Sprintf("⚠️ Процесс бэкапа отменен: сервер работает от батареи (%d%%, Discharging). Резервное копирование выполняется только при питании от сети.", cap)
		log.Println(msg)
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: msg}
		return
	}

	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "⚠️ Бэкап уже выполняется в другом потоке. Новый запуск отменен."}
		return
	}
	s.isRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isRunning = false
		s.mu.Unlock()
	}()

	s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "🚀 Запущен регламентный бэкап диска стореджа."}

	globalCtx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()

	var logBuilder strings.Builder

	rawDev, err := s.sysRunner.RunCmdWithContext(globalCtx, "blkid", "-c", "/dev/null", "-U", s.cfg.Backup.UUID)
	discDev := strings.TrimSpace(rawDev)

	if err != nil || discDev == "" || strings.Contains(discDev, "exit status") {
		logBuilder.WriteString("⚡ Диск не обнаружен. Перезапускаем питание USB 3.0 (xhci_hcd)...\n")

		unbindCmd := fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/unbind", s.cfg.Backup.XhciPCI)
		_, _ = s.sysRunner.RunCmdWithContext(globalCtx, "sh", "-c", unbindCmd)

		select {
		case <-time.After(5 * time.Second):
		case <-globalCtx.Done():
			s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "❌ Ошибка: Превышен таймаут при перезапуске USB порта."}
			return
		}

		bindCmd := fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/bind", s.cfg.Backup.XhciPCI)
		_, _ = s.sysRunner.RunCmdWithContext(globalCtx, "sh", "-c", bindCmd)

		select {
		case <-time.After(5 * time.Second):
		case <-globalCtx.Done():
			return
		}

		rawDev, err = s.sysRunner.RunCmdWithContext(globalCtx, "blkid", "-c", "/dev/null", "-U", s.cfg.Backup.UUID)
		discDev = strings.TrimSpace(rawDev)
	}

	if err != nil || discDev == "" || strings.Contains(discDev, "exit status") {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "❌ Ошибка бэкапа: Внешний жесткий диск не найден! Проверьте USB-кабель."}
		return
	}

	if _, err := s.sysRunner.RunCmdWithContext(globalCtx, "mkdir", "-p", s.cfg.Backup.MountPoint); err != nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка создания папки монтирования: %v", err)}
		return
	}

	mountCheck, err := s.sysRunner.RunCmdWithContext(globalCtx, "findmnt", "-M", s.cfg.Backup.MountPoint)
	if err != nil || strings.TrimSpace(mountCheck) == "" {
		_, err = s.sysRunner.RunCmdWithContext(globalCtx, "mount", discDev, s.cfg.Backup.MountPoint)
		if err != nil {
			s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка монтирования: %v", err)}
			return
		}
	}

	realTargetDev, err := s.sysRunner.RunCmdWithContext(globalCtx, "findmnt", "-n", "-o", "SOURCE", "-M", s.cfg.Backup.MountPoint)
	if err != nil || !strings.Contains(strings.TrimSpace(realTargetDev), discDev) {
		s.eventChan <- &pb.StreamEventsResponse{
			Type:    "backup_status",
			Message: "❌ КРИТИЧЕСКАЯ ОШИБКА: Защита сработала. Папка не указывает на USB-диск! Бэкап заблокирован.",
		}
		return
	}

	logBuilder.WriteString("🔄 Запущена синхронизация rsync...\n")

	sourceDir := filepath.Clean(s.cfg.Backup.SourceDir) + "/"
	targetDir := filepath.Clean(s.cfg.Backup.MountPoint) + "/"

	rsyncOut, rsyncErr := s.sysRunner.RunCmdWithContext(globalCtx, "rsync", "-aHAX", "--delete", sourceDir, targetDir)
	logBuilder.WriteString(rsyncOut + "\n")

	if rsyncErr != nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка выполнения rsync (или таймаут):\n%s", logBuilder.String())}
	}

	_, _ = s.sysRunner.RunCmdWithContext(context.Background(), "umount", "-l", s.cfg.Backup.MountPoint)

	diskBase := getBaseDevice(discDev)
	if diskBase != "" {
		powerOffCtx, powerOffCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer powerOffCancel()

		_, _ = s.sysRunner.RunCmdWithContext(powerOffCtx, "udisksctl", "power-off", "-b", diskBase)
	}

	if rsyncErr == nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "✅ Резервное копирование успешно завершено. Внешний диск обесточен."}
	}
}
