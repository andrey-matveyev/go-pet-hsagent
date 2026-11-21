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
	sysDisk, _ := s.getDiskStat("/")
	storageDisk, _ := s.getDiskStat(s.cfg.Backup.SourceDir)

	var disks []*pb.DiskUsage
	if sysDisk != nil {
		disks = append(disks, &pb.DiskUsage{
			Name:     "SSD",
			UsedGb:   sysDisk.UsedGB,
			TotalGb:  sysDisk.TotalGB,
			UsagePct: sysDisk.UsagePct,
		})
	}
	if storageDisk != nil {
		disks = append(disks, &pb.DiskUsage{
			Name:     "HDD",
			UsedGb:   storageDisk.UsedGB,
			TotalGb:  storageDisk.TotalGB,
			UsagePct: storageDisk.UsagePct,
		})
	}

	// Получаем аптайм сервера
	uptime, _ := s.getUptime()

	// Формируем временные метки
	now := time.Now().Format(time.RFC3339)

	// Получаем информацию о загрузке CPU по ядрам
	cpuUsages, cpuErr := s.getCPUUsage()
	var cpuUsagePb []*pb.CPUUsage
	if cpuErr == nil {
		for _, cu := range cpuUsages {
			cpuUsagePb = append(cpuUsagePb, &pb.CPUUsage{
				CoreId:   cu.CoreID,
				UsagePct: cu.Usage,
			})
		}
	}

	// Получаем информацию о памяти
	memInfo, memErr := s.getMemoryInfo()
	var ramPb, swapPb *pb.MemoryInfo
	if memErr == nil {
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
		Timestamp:       now,
		Disks:           disks,
		Ram:             ramPb,
		Swap:            swapPb,
		CpuUsage:        cpuUsagePb,
		Uptime:          uptime,
	}, nil
}

// Получение информации о загрузке CPU по ядрам из /proc/stat
type CPUCoreUsage struct {
	CoreID string
	Usage  float64
}

func (s *server) getCPUUsage() ([]CPUCoreUsage, error) {
	readStats := func() (map[string]struct{ idle, total float64 }, error) {
		data, err := s.sysRunner.ReadFile("/proc/stat")
		if err != nil {
			return nil, err
		}
		res := make(map[string]struct{ idle, total float64 })
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "cpu") {
				continue
			}
			fields := strings.Fields(line)
			coreName := fields[0] // "cpu", "cpu0", "cpu1"

			var total float64
			for _, f := range fields[1:] {
				v, _ := strconv.ParseFloat(f, 64)
				total += v
			}
			idle, _ := strconv.ParseFloat(fields[4], 64) // idle time
			res[coreName] = struct{ idle, total float64 }{idle: idle, total: total}
		}
		return res, nil
	}

	stat1, err := readStats()
	if err != nil {
		return nil, err
	}
	time.Sleep(200 * time.Millisecond) // Короткая пауза между замерами
	stat2, err := readStats()
	if err != nil {
		return nil, err
	}

	var results []CPUCoreUsage
	for core, s1 := range stat1 {
		s2, ok := stat2[core]
		if !ok {
			continue
		}
		idleDelta := s2.idle - s1.idle
		totalDelta := s2.total - s1.total

		usage := 0.0
		if totalDelta > 0 {
			usage = (1.0 - idleDelta/totalDelta) * 100
		}

		if core == "cpu" {
			core = "Overall"
		}
		results = append(results, CPUCoreUsage{CoreID: core, Usage: usage})
	}
	return results, nil
}

// Получение информации о RAM и Swap из /proc/meminfo
type MemInfo struct {
	TotalRAMBytes  float64
	UsedRAMBytes   float64
	RAMUsagePct    float64
	TotalSwapBytes float64
	UsedSwapBytes  float64
	SwapUsagePct   float64
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
		TotalRAMBytes:  memTotal,
		UsedRAMBytes:   usedRAM,
		RAMUsagePct:    ramPct,
		TotalSwapBytes: swapTotal,
		UsedSwapBytes:  usedSwap,
		SwapUsagePct:   swapPct,
	}, nil
}

// Получение аптайма сервера из /proc/uptime
func (s *server) getUptime() (string, error) {
	data, err := s.sysRunner.ReadFile("/proc/uptime")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty /proc/uptime")
	}
	uptimeSeconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", err
	}

	totalMinutes := int(uptimeSeconds) / 60
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes % (24 * 60)) / 60
	minutes := totalMinutes % 60

	if days > 0 {
		return fmt.Sprintf("%dd / %dh", days, hours), nil
	}
	return fmt.Sprintf("%dh / %dm", hours, minutes), nil
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
