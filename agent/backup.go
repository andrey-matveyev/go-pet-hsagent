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
		msg := fmt.Sprintf("Backup cancelled: server running on battery (%d%%, Discharging). Backup only runs on AC power.", cap)
		log.Println(msg)
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: msg}
		return
	}

	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "Backup already running in another thread. New launch cancelled."}
		return
	}
	s.isRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isRunning = false
		s.mu.Unlock()
	}()

	globalCtx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()

	var logBuilder strings.Builder

	rawDev, err := s.sysRunner.RunCmdWithContext(globalCtx, "blkid", "-c", "/dev/null", "-U", s.cfg.Backup.TargetFilesystemUUID)
	discDev := strings.TrimSpace(rawDev)

	if err != nil || discDev == "" || strings.Contains(discDev, "exit status") {
		logBuilder.WriteString("Disk not detected. Restarting USB 3.0 power (xhci_hcd)...\n")

		if _, err := s.setXHCIBind(s.cfg.Backup.TargetXhciPCI, false, globalCtx); err != nil {
			log.Printf("⚠️ Не удалось отключить USB порт бэкапа (PCI %s): %v", s.cfg.Backup.TargetXhciPCI, err)
		}

		select {
		case <-time.After(5 * time.Second):
		case <-globalCtx.Done():
			s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "Error: Timeout while restarting USB port."}
			return
		}

		if _, err := s.setXHCIBind(s.cfg.Backup.TargetXhciPCI, true, globalCtx); err != nil {
			log.Printf("⚠️ Не удалось включить USB порт бэкапа (PCI %s): %v", s.cfg.Backup.TargetXhciPCI, err)
		}

		select {
		case <-time.After(5 * time.Second):
		case <-globalCtx.Done():
			return
		}

		rawDev, err = s.sysRunner.RunCmdWithContext(globalCtx, "blkid", "-c", "/dev/null", "-U", s.cfg.Backup.TargetFilesystemUUID)
		discDev = strings.TrimSpace(rawDev)
	}

	if err != nil || discDev == "" || strings.Contains(discDev, "exit status") {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "Backup error: External hard drive not found! Check USB cable."}
		return
	}

	if _, err := s.sysRunner.RunCmdWithContext(globalCtx, "mkdir", "-p", s.cfg.Backup.TargetMountPoint); err != nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("Error creating mount point: %v", err)}
		return
	}

	mountCheck, err := s.sysRunner.RunCmdWithContext(globalCtx, "findmnt", "-M", s.cfg.Backup.TargetMountPoint)
	if err != nil || strings.TrimSpace(mountCheck) == "" {
		_, err = s.sysRunner.RunCmdWithContext(globalCtx, "mount", discDev, s.cfg.Backup.TargetMountPoint)
		if err != nil {
			s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("Error mounting: %v", err)}
			return
		}
	}

	realTargetDev, err := s.sysRunner.RunCmdWithContext(globalCtx, "findmnt", "-n", "-o", "SOURCE", "-M", s.cfg.Backup.TargetMountPoint)
	if err != nil || !strings.Contains(strings.TrimSpace(realTargetDev), discDev) {
		s.eventChan <- &pb.StreamEventsResponse{
			Type:    "backup_status",
			Message: "Critical error: Protection triggered. Folder does not point to USB drive! Backup blocked.",
		}
		return
	}

	logBuilder.WriteString("Starting rsync synchronization...\n")

	sourceDir := filepath.Clean(s.cfg.Backup.SourceDirectory) + "/"
	targetDir := filepath.Clean(s.cfg.Backup.TargetMountPoint) + "/"

	rsyncOut, rsyncErr := s.sysRunner.RunCmdWithContext(globalCtx, "rsync", "-aHAX", "--delete", sourceDir, targetDir)
	logBuilder.WriteString(rsyncOut + "\n")

	if rsyncErr != nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: fmt.Sprintf("Error executing rsync (or timeout):\n%s", logBuilder.String())}
	}

	_, _ = s.sysRunner.RunCmdWithContext(context.Background(), "umount", "-l", s.cfg.Backup.TargetMountPoint)

	diskBase := getBaseDevice(discDev)
	if diskBase != "" {
		powerOffCtx, powerOffCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer powerOffCancel()

		_, _ = s.sysRunner.RunCmdWithContext(powerOffCtx, "udisksctl", "power-off", "-b", diskBase)
	}

	if rsyncErr == nil {
		s.eventChan <- &pb.StreamEventsResponse{Type: "backup_status", Message: "Backup completed successfully. External drive powered off."}
	}
}
