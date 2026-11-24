package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

type server struct {
	pb.UnimplementedMonitorServiceServer
	cfg          *config
	eventChan    chan *pb.StreamEventsResponse
	mu           sync.Mutex
	lastCap      int
	lastStat     string
	cpuAlerted   bool
	diskAlerted  map[string]bool
	rebootAlerted bool
	isRunning    bool
	sysRunner    SystemRunner
}

// GetSystemStatus возвращает текущий статус системы (батарея, CPU, диск, память, аптайм и т.д.)
func (s *server) GetSystemStatus(ctx context.Context, in *pb.GetSystemStatusRequest) (*pb.GetSystemStatusResponse, error) {
	bCap, bStat, _ := s.getBatteryInfo()
	cpuTemp, _ := s.getCpuTemperature()
	sysDisk, _ := s.getDiskStat("/")
	storageDisk, _ := s.getDiskStat(s.cfg.Backup.SourceDir)

	disks := []*pb.DiskUsage{}
	if sysDisk != nil {
		disks = append(disks, &pb.DiskUsage{
			Name:     getDiskType("/"),
			UsedGb:   sysDisk.UsedGB,
			TotalGb:  sysDisk.TotalGB,
			UsagePct: sysDisk.UsagePct,
		})
	}
	if storageDisk != nil {
		disks = append(disks, &pb.DiskUsage{
			Name:     getDiskType(s.cfg.Backup.SourceDir),
			UsedGb:   storageDisk.UsedGB,
			TotalGb:  storageDisk.TotalGB,
			UsagePct: storageDisk.UsagePct,
		})
	}

	uptime, _ := s.getUptime()
	cpuUsages, _ := s.getCPUUsage()

	cpuUsagePb := make([]*pb.CPUUsage, 0, len(cpuUsages))
	for _, cu := range cpuUsages {
		cpuUsagePb = append(cpuUsagePb, &pb.CPUUsage{
			CoreId:   cu.CoreID,
			UsagePct: cu.Usage,
		})
	}

	memInfo, _ := s.getMemoryInfo()
	var ramPb, swapPb *pb.MemoryInfo
	if memInfo != nil {
		ramPb = &pb.MemoryInfo{
			TotalBytes: memInfo.TotalRAMBytes,
			UsedBytes:  memInfo.UsedRAMBytes,
			UsagePct:   memInfo.RAMUsagePct,
		}
		swapPb = &pb.MemoryInfo{
			TotalBytes: memInfo.TotalSwapBytes,
			UsedBytes:  memInfo.UsedSwapBytes,
			UsagePct:   memInfo.SwapUsagePct,
		}
	}

	return &pb.GetSystemStatusResponse{
		BatteryCapacity: int32(bCap),
		BatteryStatus:   bStat,
		CpuTemperature:  cpuTemp,
		Timestamp:       time.Now().Format(time.RFC3339),
		Disks:           disks,
		Ram:             ramPb,
		Swap:            swapPb,
		CpuUsage:        cpuUsagePb,
		Uptime:          uptime,
		ServerModel:     getServerModel(),
		RebootRequired:  needsReboot(),
	}, nil
}

// StreamEvents стримит события мониторинга в подключённый gRPC-поток
func (s *server) StreamEvents(in *pb.StreamEventsRequest, stream pb.MonitorService_StreamEventsServer) error {
	log.Println("🤖 Бот подключился к gRPC стриму событий")
	for event := range s.eventChan {
		if err := stream.Send(event); err != nil {
			log.Printf("❌ Ошибка отправки события боту: %v", err)
			return err
		}
	}
	return nil
}

// TestSystems отправляет тестовое событие в стрим (команда /test)
func (s *server) TestSystems(ctx context.Context, in *pb.TestSystemsRequest) (*pb.TestSystemsResponse, error) {
	log.Println("🔍 Запущен тест систем...")
	s.eventChan <- &pb.StreamEventsResponse{
		Type:    "test",
		Message: "Test notification: gRPC event stream is working correctly!",
	}
	return &pb.TestSystemsResponse{
		ResultMessage: "Test event successfully sent to stream.",
	}, nil
}

// TriggerBackup запускает бэкап по команде из Telegram (команда /backup)
func (s *server) TriggerBackup(ctx context.Context, in *pb.TriggerBackupRequest) (*pb.TriggerBackupResponse, error) {
	log.Println("🚀 Команда на принудительный запуск бэкапа из Telegram")

	if cap, stat, err := s.getBatteryInfo(); err == nil && stat == "Discharging" {
		msg := fmt.Sprintf("Backup cancelled: server running on battery (%d%%, Discharging). Backup only runs on AC power.", cap)
		log.Println(msg)
		return &pb.TriggerBackupResponse{ResultMessage: msg}, nil
	}

	go s.runBackupRoutine()
	return &pb.TriggerBackupResponse{
		ResultMessage: "Forced storage backup started via Telegram command.",
	}, nil
}

// setXHCIBind привязывает/отвязывает xHCI PCI-устройство (включение/выключение питания USB-порта)
func (s *server) setXHCIBind(pciAddr string, bind bool, ctx context.Context) (string, error) {
	action := "bind"
	if !bind {
		action = "unbind"
	}
	pciAddr = strings.TrimSpace(pciAddr)
	if pciAddr == "" {
		return "", fmt.Errorf("xHCI PCI address is empty")
	}
	cmd := fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/%s", pciAddr, action)
	return s.sysRunner.RunCmdWithContext(ctx, "sh", "-c", cmd)
}

// Reboot инициирует перезагрузку сервера (команда /reboot)
func (s *server) Reboot(ctx context.Context, in *pb.RebootRequest) (*pb.RebootResponse, error) {
	log.Println("🔄 Команда на перезагрузку сервера из Telegram")

	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return &pb.RebootResponse{
			ResultMessage: "Reboot cancelled: backup is currently running. Please try again later.",
		}, nil
	}
	s.mu.Unlock()

	go func() {
		out, err := s.sysRunner.RunCmdWithContext(context.Background(), "/sbin/shutdown", "-r", "now", "Reboot requested via Telegram Bot")
		if err != nil {
			log.Printf("❌ Ошибка перезагрузки: %v, output: %s", err, out)
		} else {
			log.Printf("✅ Перезагрузка запущена: %s", out)
		}
	}()
	return &pb.RebootResponse{
		ResultMessage: "Server reboot initiated immediately.",
	}, nil
}
