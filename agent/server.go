package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

type server struct {
	pb.UnimplementedMonitorServiceServer
	cfg         *config
	eventChan   chan *pb.StreamEventsResponse
	mu          sync.Mutex
	lastCap     int
	lastStat    string
	cpuAlerted  bool
	diskAlerted map[string]bool
	isRunning   bool

	// Абстракция для выполнения системных функций
	sysRunner SystemRunner
}

// gRPC Метод: Получение системного статуса
func (s *server) GetSystemStatus(ctx context.Context, in *pb.GetSystemStatusRequest) (*pb.GetSystemStatusResponse, error) {
	bCap, bStat, _ := s.getBatteryInfo()
	cpuTemp, _ := s.getCpuTemperature()
	_, sysDiskReport, _ := s.getDiskUsage("/")
	_, storageDiskReport, _ := s.getDiskUsage(s.cfg.Backup.SourceDir)

	diskReport := fmt.Sprintf("💻 Системный SSD: %s\n📸 Хранилище Immich: %s", sysDiskReport, storageDiskReport)

	// Формируем временные метки
	now := time.Now().Format(time.RFC3339)

	// Получаем информацию о памяти
	memInfo, memErr := s.getMemoryInfo()
	var ramPb, swapPb *pb.GetSystemStatusResponse_MemoryInfo
	if memErr == nil {
		ramPb = &pb.GetSystemStatusResponse_MemoryInfo{
			TotalBytes: memInfo.TotalRAMBytes,
			UsedBytes:  memInfo.UsedRAMBytes,
			UsagePct:   memInfo.RAMUsagePct,
		}
		swapPb = &pb.GetSystemStatusResponse_MemoryInfo{
			TotalBytes: memInfo.TotalSwapBytes,
			UsedBytes:  memInfo.UsedSwapBytes,
			UsagePct:   memInfo.SwapUsagePct,
		}
	}

	return &pb.GetSystemStatusResponse{
		BatteryCapacity: int32(bCap),
		BatteryStatus:   bStat,
		CpuTemperature:  cpuTemp,
		DiskUsageInfo:   diskReport,
		Timestamp:       now, // Добавленное поле с временной меткой
		Ram:             ramPb,
		Swap:            swapPb,
	}, nil
}

// Получение информации о RAM и Swap из /proc/meminfo
type MemInfo struct {
	TotalRAMBytes    float64
	UsedRAMBytes     float64
	RAMUsagePct      float64
	TotalSwapBytes   float64
	UsedSwapBytes    float64
	SwapUsagePct     float64
}

func (s *server) getMemoryInfo() (*MemInfo, error) {
	data, err := s.sysRunner.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, err
	}

	var memTotal, memAvailable, swapTotal, swapFree float64

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseFloat(fields[1], 64) // значения в kB
		valBytes := val * 1024

		switch fields[0] {
		case "MemTotal:":
			memTotal = valBytes
		case "MemAvailable:":
			memAvailable = valBytes
		case "SwapTotal:":
			swapTotal = valBytes
		case "SwapFree:":
			swapFree = valBytes
		}
	}

	usedRAM := memTotal - memAvailable
	ramPct := 0.0
	if memTotal > 0 {
		ramPct = (usedRAM / memTotal) * 100
	}

	usedSwap := swapTotal - swapFree
	swapPct := 0.0
	if swapTotal > 0 {
		swapPct = (usedSwap / swapTotal) * 100
	}

	return &MemInfo{
		TotalRAMBytes:   memTotal,
		UsedRAMBytes:    usedRAM,
		RAMUsagePct:     ramPct,
		TotalSwapBytes:  swapTotal,
		UsedSwapBytes:   usedSwap,
		SwapUsagePct:    swapPct,
	}, nil
}

// gRPC Метод: Стрим алармов
func (s *server) StreamEvents(in *pb.StreamEventsRequest, stream pb.MonitorService_StreamEventsServer) error {
	log.Println("🤖 Бот успешно подключился к gRPC стриму событий")
	for event := range s.eventChan {
		if err := stream.Send(event); err != nil {
			log.Printf("❌ Ошибка отправки события боту: %v", err)
			return err
		}
	}
	return nil
}

// gRPC Метод: Тест системы (/test)
func (s *server) TestSystems(ctx context.Context, in *pb.TestSystemsRequest) (*pb.TestSystemsResponse, error) {
	log.Println("🔍 Запущен принудительный тест систем...")

	s.eventChan <- &pb.StreamEventsResponse{
		Type:    "test",
		Message: "🔔 Тестовое уведомление: gRPC-стриминг работает корректно!",
	}

	return &pb.TestSystemsResponse{
		ResultMessage: "Тестовое событие успешно отправлено в стрим.",
	}, nil
}

// gRPC Метод: Ручной запуск бэкапа (/backup)
func (s *server) TriggerBackup(ctx context.Context, in *pb.TriggerBackupRequest) (*pb.TriggerBackupResponse, error) {
	log.Println("🚀 Получена команда из Telegram на принудительный запуск бэкапа...")

	// Запускаем бэкап асинхронно в фоне (в том же стиле, что и регламентный)
	go s.runBackupRoutine()

	return &pb.TriggerBackupResponse{
		ResultMessage: "🚀 Запущен принудительный бэкап хранилища по команде из Telegram.",
	}, nil
}
