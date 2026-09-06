package main

import (
	"fmt"
	"strings"
	"time"

	pb "go-pet-hsagent/proto"
)

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
