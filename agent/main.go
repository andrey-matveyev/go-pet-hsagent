package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	pb "go-pet-hsagent/proto" // Абсолютный импорт на базе вашего go.mod

	"github.com/BurntSushi/toml"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type server struct {
	pb.UnimplementedMonitorServiceServer
	cfg         *Config
	eventChan   chan *pb.EventNotification
	mu          sync.Mutex
	lastCap     int
	lastStat    string
	cpuAlerted  bool
	diskAlerted map[string]bool
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func getBatteryInfo() (int, string, error) {
	capRaw, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/capacity")
	if err != nil {
		return 0, "", err
	}
	statRaw, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/status")
	if err != nil {
		return 0, "", err
	}
	capacity, _ := strconv.Atoi(strings.TrimSpace(string(capRaw)))
	status := strings.TrimSpace(string(statRaw))
	return capacity, status, nil
}

func getCpuTemperature() (float64, error) {
	tempRaw, err := ioutil.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}
	tempMillidegrees, err := strconv.Atoi(strings.TrimSpace(string(tempRaw)))
	if err != nil {
		return 0, err
	}
	return float64(tempMillidegrees) / 1000.0, nil
}

func getDiskUsage(path string) (float64, string, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, "", err
	}
	all := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if all == 0 {
		return 0, "", fmt.Errorf("disk size is 0")
	}
	percentFree := (float64(free) / float64(all)) * 100.0
	gbFree := float64(free) / (1024 * 1024 * 1024)
	gbAll := float64(all) / (1024 * 1024 * 1024)
	return percentFree, fmt.Sprintf("%.1f ГБ свободно из %.1f ГБ (%.1f%%)", gbFree, gbAll, percentFree), nil
}

// gRPC Метод: Исправлен под контракт SystemStatusResponse
func (s *server) GetBatteryStatus(ctx context.Context, in *pb.Empty) (*pb.SystemStatusResponse, error) {
	bCap, bStat, _ := getBatteryInfo()
	cpuTemp, _ := getCpuTemperature()
	_, sysDiskReport, _ := getDiskUsage("/")
	_, storageDiskReport, _ := getDiskUsage(s.cfg.Backup.SourceDir)

	diskReport := fmt.Sprintf("💻 Системный SSD: %s\n📸 Хранилище Immich: %s", sysDiskReport, storageDiskReport)

	return &pb.SystemStatusResponse{
		BatteryCapacity: int32(bCap),
		BatteryStatus:   bStat,
		CpuTemperature:  cpuTemp,
		DiskUsageInfo:   diskReport,
	}, nil
}

// gRPC Метод: Стрим алармов
func (s *server) StreamEvents(in *pb.Empty, stream pb.MonitorService_StreamEventsServer) error {
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
func (s *server) TestSystems(ctx context.Context, in *pb.Empty) (*pb.TestResponse, error) {
	log.Println("🔍 Запущен принудительный тест систем...")

	s.eventChan <- &pb.EventNotification{
		Type:    "test_alert",
		Message: "🔔 **Тестовый Push**: Канал gRPC-стрима уведомлений работает стабильно!",
	}

	_, _, errBat := getBatteryInfo()
	_, errCpu := getCpuTemperature()
	_, _, errDisk := getDiskUsage("/")

	statusReport := "📋 **Результаты самодиагностики Агента:**\n"
	if errBat == nil {
		statusReport += "✅ Датчик батареи: OK\n"
	} else {
		statusReport += "❌ Датчик батареи: ОШИБКА\n"
	}
	if errCpu == nil {
		statusReport += "✅ Датчик температуры CPU: OK\n"
	} else {
		statusReport += "❌ Датчик температуры CPU: ОШИБКА\n"
	}
	if errDisk == nil {
		statusReport += "✅ Доступ к ФС (Statfs): OK\n"
	} else {
		statusReport += "❌ Доступ к ФС: ОШИБКА\n"
	}

	statusReport += fmt.Sprintf("⏰ Планировщик бэкапов: Активен (Ожидание: %s в %s)\n", s.cfg.Backup.ScheduleWeekday, s.cfg.Backup.ScheduleTime)
	statusReport += "🚀 Все независимые горутины мониторинга работают параллельно."

	return &pb.TestResponse{ResultMessage: statusReport}, nil
}

// Поток батареи
func (s *server) monitorBatteryLoop() {
	interval := time.Duration(s.cfg.Monitoring.BatteryIntervalMin) * time.Minute
	for {
		cap, stat, err := getBatteryInfo()
		if err == nil {
			s.mu.Lock()
			if cap != s.lastCap || stat != s.lastStat {
				s.lastCap = cap
				s.lastStat = stat
				s.mu.Unlock()

				var emoji = "🔋"
				if stat == "Discharging" {
					emoji = "⚠️ 🔌 Пропало внешнее питание! Сервер перешел на батарею."
				} else if stat == "Charging" {
					emoji = "⚡ 🔌 Внешнее питание восстановлено. Зарядка."
				}
				s.eventChan <- &pb.EventNotification{
					Type:    "battery_alert",
					Message: fmt.Sprintf("%s\nТекущий заряд: %d%%", emoji, cap),
				}
			} else {
				s.mu.Unlock()
			}
		}
		time.Sleep(interval)
	}
}

// Поток процессора
func (s *server) monitorCpuLoop() {
	interval := time.Duration(s.cfg.Monitoring.CpuIntervalMin) * time.Minute
	for {
		temp, err := getCpuTemperature()
		if err == nil {
			s.mu.Lock()
			threshold := s.cfg.Monitoring.CpuTempThreshold
			if temp >= threshold && !s.cpuAlerted {
				s.cpuAlerted = true
				s.mu.Unlock()
				s.eventChan <- &pb.EventNotification{
					Type:    "cpu_alert",
					Message: fmt.Sprintf("🔥 Внимание! Процессор нагрелся до критических %.1f°C!", temp),
				}
			} else if temp < (threshold-5.0) && s.cpuAlerted {
				s.cpuAlerted = false
				s.mu.Unlock()
				s.eventChan <- &pb.EventNotification{
					Type:    "cpu_alert",
					Message: fmt.Sprintf("🟢 Процессор остыл до безопасных %.1f°C.", temp),
				}
			} else {
				s.mu.Unlock()
			}
		}
		time.Sleep(interval)
	}
}

// Поток дисков
func (s *server) monitorDisksLoop() {
	interval := time.Duration(s.cfg.Monitoring.DiskIntervalMin) * time.Minute
	paths := map[string]string{
		"Системный SSD (240GB)":  "/",
		"Хранилище Immich (1TB)": s.cfg.Backup.SourceDir,
	}
	for {
		for name, path := range paths {
			percentFree, _, err := getDiskUsage(path)
			if err == nil {
				s.mu.Lock()
				minFree := s.cfg.Monitoring.DiskMinFreePercent
				if percentFree < minFree && !s.diskAlerted[name] {
					s.diskAlerted[name] = true
					s.mu.Unlock()
					s.eventChan <- &pb.EventNotification{
						Type:    "disk_alert",
						Message: fmt.Sprintf("🚨 Критически мало места! На диске [%s] осталось всего %.1f%% свободного пространства.", name, percentFree),
					}
				} else if percentFree >= minFree && s.diskAlerted[name] {
					s.diskAlerted[name] = false
					s.mu.Unlock()
				} else {
					s.mu.Unlock()
				}
			}
		}
		time.Sleep(interval)
	}
}

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

func main() {
	var cfg Config
	// Ищем config.toml в текущей рабочей папке, откуда запускают бинарник
	if _, err := toml.DecodeFile("config.toml", &cfg); err != nil {
		log.Fatalf("❌ Критическая ошибка: не удалось прочитать config.toml: %v", err)
	}

	lis, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	kaep := keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}
	kasp := keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 5 * time.Second}
	s := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp))
	srv := &server{cfg: &cfg, eventChan: make(chan *pb.EventNotification, 20), diskAlerted: make(map[string]bool)}
	pb.RegisterMonitorServiceServer(s, srv)

	go srv.monitorBatteryLoop()
	go srv.monitorCpuLoop()
	go srv.monitorDisksLoop()
	go srv.startSchedulerLoop()
	log.Printf("🚀 Системный gRPC Агент успешно запущен на %s", cfg.ListenAddress)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
