package main

import (
	"context"
	"fmt"
	"log"
	"os"
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

	rebootRequired, _ := needsReboot()

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
		ServerModel:     getServerModel(),
		RebootRequired:  rebootRequired,
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

// getDiskType определяет тип диска (SSD/HDD) по точке монтирования,
// читая /sys/block/<device>/queue/rotational.
func getDiskType(mountPath string) string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "Unknown"
	}

	var deviceName string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// fields[0] = устройство, fields[1] = точка монтирования
		if fields[1] == mountPath {
			deviceName = fields[0]
			break
		}
	}

	if deviceName == "" {
		return "Unknown"
	}

	// Убираем /dev/ префикс
	deviceName = strings.TrimPrefix(deviceName, "/dev/")

	// Извлекаем базовое имя устройства (убираем номер раздела)
	baseDevice := extractBaseDevice(deviceName)

	rotationalPath := fmt.Sprintf("/sys/block/%s/queue/rotational", baseDevice)
	rotRaw, err := os.ReadFile(rotationalPath)
	if err != nil {
		return "Unknown"
	}

	rotational := strings.TrimSpace(string(rotRaw))
	if rotational == "0" {
		return "SSD"
	}
	return "HDD"
}

// extractBaseDevice извлекает базовое имя блочного устройства из имени раздела.
// Например: sda1 -> sda, nvme0n1p1 -> nvme0n1, sda -> sda.
func extractBaseDevice(device string) string {
	// Для NVMe устройств: nvme0n1p1 -> nvme0n1
	if strings.HasPrefix(device, "nvme") {
		idx := strings.LastIndex(device, "p")
		if idx > 0 {
			if _, err := strconv.Atoi(device[idx+1:]); err == nil {
				return device[:idx]
			}
		}
		return device
	}

	// Для sdX, hdX, vdX и подобных: убираем trailing digits (sda1 -> sda)
	for i := len(device) - 1; i >= 0; i-- {
		if device[i] < '0' || device[i] > '9' {
			return device[:i+1]
		}
	}
	return device
}

// getServerModel читает актуальную модель железа из DMI (Linux)
func getServerModel() string {
	data, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil {
		log.Printf("⚠️ Не удалось прочитать модель сервера из DMI: %v", err)
		return ""
	}
	return strings.TrimSpace(string(data))
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

// disableBackupUSB отключает питание USB-порта с бэкап-диском,
// unbinding соответствующего xHCI PCI-устройства из драйвера xhci_hcd.
// Вызывается при старте агента (включая после перезагрузки ОС),
// чтобы порт оставался выключенным. Остальные USB-порты не затрагиваются.
func (s *server) disableBackupUSB() {
	pciAddr := strings.TrimSpace(s.cfg.Backup.XhciPCI)
	if pciAddr == "" {
		log.Println("⚠️ backup.xhci_pci не задан в конфиге — пропускаем отключение USB порта бэкапа.")
		return
	}

	unbindCmd := fmt.Sprintf("echo '%s' > /sys/bus/pci/drivers/xhci_hcd/unbind", pciAddr)
	out, err := s.sysRunner.RunCmdWithContext(context.Background(), "sh", "-c", unbindCmd)
	if err != nil {
		log.Printf("⚠️ Не удалось отключить USB порт бэкапа (PCI %s): %v, output: %s", pciAddr, err, out)
		return
	}
	log.Printf("✅ USB порт бэкапа (PCI %s) отключён. Останется выключенным до повторного включения.", pciAddr)
}

// gRPC Метод: Перезагрузка сервера (/reboot)
func (s *server) Reboot(ctx context.Context, in *pb.RebootRequest) (*pb.RebootResponse, error) {
	log.Println("🔄 Получена команда из Telegram на перезагрузку сервера...")

	go func() {
		out, err := s.sysRunner.RunCmdWithContext(ctx, "sudo", "/sbin/shutdown", "-r", "+1", "Reboot requested via Telegram Bot")
		if err != nil {
			log.Printf("❌ Ошибка при выполнении перезагрузки: %v, output: %s", err, out)
		} else {
			log.Printf("✅ Команда перезагрузки выполнена: %s", out)
		}
	}()

	return &pb.RebootResponse{
		ResultMessage: "🔄 Перезагрузка сервера инициирована через Telegram (через 1 минуту).",
	}, nil
}
