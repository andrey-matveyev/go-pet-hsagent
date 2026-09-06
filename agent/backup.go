package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	pb "go-pet-hsagent/proto"
)

/*
// Поток бэкапа
func (s *server) runBackupRoutine() {
	s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "🚀 Запущен регламентный бэкап диска стореджа."}
	var logBuilder strings.Builder
	discDev, err := runCmd("blkid", "-U", s.cfg.Backup.UUID)

	if err != nil || discDev == "" {
		logBuilder.WriteString("⚡ Диск не обнаружен. Пробуем подать питание на USB 3.0 порт...\n")
		runCmd("sh", "-c", fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/unbind", s.cfg.Backup.XhciPCI))
		time.Sleep(1 * time.Second)
		runCmd("sh", "-c", fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/bind", s.cfg.Backup.XhciPCI))
		time.Sleep(5 * time.Second)
		discDev, _ = runCmd("blkid", "-U", s.cfg.Backup.UUID)
	}

	if discDev == "" {
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "❌ Ошибка бэкапа: Внешний жесткий диск не найден! Проверьте USB-кабель."}
		return
	}

	runCmd("mkdir", "-p", s.cfg.Backup.MountPoint)
	mountCheck, _ := runCmd("mountpoint", "-q", s.cfg.Backup.MountPoint)
	if mountCheck != "" {
		_, err = runCmd("mount", discDev, s.cfg.Backup.MountPoint)
		if err != nil {
			s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка монтирования: %v", err)}
			return
		}
	}

	logBuilder.WriteString("🔄 Запущена синхронизация rsync...\n")
	rsyncOut, err := runCmd("rsync", "-aHAX", "--delete", s.cfg.Backup.SourceDir, s.cfg.Backup.MountPoint+"/")
	logBuilder.WriteString(rsyncOut + "\n")

	if err != nil {
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка выполнения rsync:\n%s", logBuilder.String())}
		return
	}

	runCmd("umount", s.cfg.Backup.MountPoint)
	diskBase := strings.TrimRight(discDev, "0123456789")
	runCmd("udisksctl", "power-off", "-b", diskBase)

	s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "✅ Резервное копирование успешно завершено. Внешний диск обесточен."}
}
*/

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
	// 0. Защита от Race Condition и параллельного запуска
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "⚠️ Бэкап уже выполняется в другом потоке. Новый запуск отменен."}
		return
	}
	s.isRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isRunning = false
		s.mu.Unlock()
	}()

	s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "🚀 Запущен регламентный бэкап диска стореджа."}

	// Создаем общий контекст на всю операцию (например, жесткий лимит 4 часа на весь бэкап)
	globalCtx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()

	var logBuilder strings.Builder

	// 1. Поиск диска (сброс питания USB при необходимости)
	rawDev, err := runCmdWithContext(globalCtx, "blkid", "-c", "/dev/null", "-U", s.cfg.Backup.UUID)
	discDev := strings.TrimSpace(rawDev)

	if err != nil || discDev == "" || strings.Contains(discDev, "exit status") {
		logBuilder.WriteString("⚡ Диск не обнаружен. Перезапускаем питание USB 3.0 (xhci_hcd)...\n")

		// Перезапуск питания с короткими таймаутами
		unbindCmd := fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/unbind", s.cfg.Backup.XhciPCI)
		_, _ = runCmdWithContext(globalCtx, "sh", "-c", unbindCmd)

		select {
		case <-time.After(5 * time.Second):
		case <-globalCtx.Done():
			s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "❌ Ошибка: Превышен таймаут при перезапуске USB порта."}
			return
		}

		bindCmd := fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/bind", s.cfg.Backup.XhciPCI)
		_, _ = runCmdWithContext(globalCtx, "sh", "-c", bindCmd)

		select {
		case <-time.After(5 * time.Second):
		case <-globalCtx.Done():
			return
		}

		// Повторный поиск
		rawDev, err = runCmdWithContext(globalCtx, "blkid", "-c", "/dev/null", "-U", s.cfg.Backup.UUID)
		discDev = strings.TrimSpace(rawDev)
	}

	if err != nil || discDev == "" || strings.Contains(discDev, "exit status") {
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "❌ Ошибка бэкапа: Внешний жесткий диск не найден! Проверьте USB-кабель."}
		return
	}

	// 2. Подготовка точки монтирования
	if _, err := runCmdWithContext(globalCtx, "mkdir", "-p", s.cfg.Backup.MountPoint); err != nil {
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка создания папки монтирования: %v", err)}
		return
	}

	// Проверяем статус монтирования
	mountCheck, err := runCmdWithContext(globalCtx, "findmnt", "-M", s.cfg.Backup.MountPoint)
	// findmnt возвращает статус > 0 (ошибку), если ничего не примонтировано. Это нормально.
	if err != nil || strings.TrimSpace(mountCheck) == "" {
		_, err = runCmdWithContext(globalCtx, "mount", discDev, s.cfg.Backup.MountPoint)
		if err != nil {
			s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка монтирования: %v", err)}
			return
		}
	}

	// 3. ЖЕЛЕЗНАЯ ЗАЩИТА СИСТЕМНОГО ДИСКА (Double Check)
	realTargetDev, err := runCmdWithContext(globalCtx, "findmnt", "-n", "-o", "SOURCE", "-M", s.cfg.Backup.MountPoint)
	if err != nil || !strings.Contains(strings.TrimSpace(realTargetDev), discDev) {
		s.eventChan <- &pb.EventNotification{
			Type:    "backup_status",
			Message: "❌ КРИТИЧЕСКАЯ ОШИБКА: Защита сработала. Папка не указывает на USB-диск! Бэкап заблокирован.",
		}
		return
	}

	// 4. Запуск rsync с защитой от зависания и правильными путями
	logBuilder.WriteString("🔄 Запущена синхронизация rsync...\n")

	// Безопасное склеивание путей (избегаем //)
	sourceDir := filepath.Clean(s.cfg.Backup.SourceDir) + "/"
	targetDir := filepath.Clean(s.cfg.Backup.MountPoint) + "/"

	// Запускаем rsync. Если rsync зависнет (диск уйдет в Read-Only), globalCtx прервет его по таймауту
	rsyncOut, rsyncErr := runCmdWithContext(globalCtx, "rsync", "-aHAX", "--delete", sourceDir, targetDir)
	logBuilder.WriteString(rsyncOut + "\n")

	if rsyncErr != nil {
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка выполнения rsync (или таймаут):\n%s", logBuilder.String())}
		// Не выходим, пытаемся безопасно отмонтировать то, что успело записаться
	}

	// 5. Корректное завершение, ленивое отмонтирование и обесточивание
	// Используем флаг -l (lazy), чтобы отмонтировать диск, даже если rsync аварийно завис и удерживает дескрипторы
	_, _ = runCmdWithContext(context.Background(), "umount", "-l", s.cfg.Backup.MountPoint)

	// Безопасное определение базового имени диска для udisksctl (поддерживает /dev/sdX, /dev/nvmeXnXpX)
	diskBase := getBaseDevice(discDev)
	if diskBase != "" {
		// Используем чистый context.Background(), чтобы команда выполнилась, даже если глобальный таймаут исчерпан
		powerOffCtx, powerOffCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer powerOffCancel()

		_, _ = runCmdWithContext(powerOffCtx, "udisksctl", "power-off", "-b", diskBase)
	}

	if rsyncErr == nil {
		s.eventChan <- &pb.EventNotification{Type: "backup_status", Message: "✅ Резервное копирование успешно завершено. Внешний диск обесточен."}
	}
}
