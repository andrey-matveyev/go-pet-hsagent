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
	// Проверка источника питания: бекап разрешен только при работе от сети (AC power).
	// Если устройство работает от батареи (статус Discharging), процесс отменяется.
	if cap, stat, err := getBatteryInfo(); err == nil && stat == "Discharging" {
		msg := fmt.Sprintf("⚠️ Процесс бэкапа отменен: сервер работает от батареи (%d%%, Discharging). Резервное копирование выполняется только при питании от сети.", cap)
		log.Println(msg)
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: msg}
		return
	}

	// 0. Защита от Race Condition и параллельного запуска
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
			s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "❌ Ошибка: Превышен таймаут при перезапуске USB порта."}
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
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "❌ Ошибка бэкапа: Внешний жесткий диск не найден! Проверьте USB-кабель."}
		return
	}

	// 2. Подготовка точки монтирования
	if _, err := runCmdWithContext(globalCtx, "mkdir", "-p", s.cfg.Backup.MountPoint); err != nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка создания папки монтирования: %v", err)}
		return
	}

	// Проверяем статус монтирования
	mountCheck, err := runCmdWithContext(globalCtx, "findmnt", "-M", s.cfg.Backup.MountPoint)
	// findmnt возвращает статус > 0 (ошибку), если ничего не примонтировано. Это нормально.
	if err != nil || strings.TrimSpace(mountCheck) == "" {
		_, err = runCmdWithContext(globalCtx, "mount", discDev, s.cfg.Backup.MountPoint)
		if err != nil {
			s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка монтирования: %v", err)}
			return
		}
	}

	// 3. ЖЕЛЕЗНАЯ ЗАЩИТА СИСТЕМНОГО ДИСКА (Double Check)
	realTargetDev, err := runCmdWithContext(globalCtx, "findmnt", "-n", "-o", "SOURCE", "-M", s.cfg.Backup.MountPoint)
	if err != nil || !strings.Contains(strings.TrimSpace(realTargetDev), discDev) {
		s.eventChan <- &pb.StreamEventsResponse{
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
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("❌ Ошибка выполнения rsync (или таймаут):\n%s", logBuilder.String())}
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
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "✅ Резервное копирование успешно завершено. Внешний диск обесточен."}
	}
}
