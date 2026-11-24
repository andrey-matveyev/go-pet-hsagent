package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SystemRunner абстракция для выполнения системных функций (чтение файлов, запуск команд)
type SystemRunner interface {
	ReadFile(filename string) ([]byte, error)
	RunCmdWithContext(ctx context.Context, name string, arg ...string) (string, error)
}

// DefaultSystemRunner дефолтная реализация SystemRunner через os/exec и os.ReadFile
type DefaultSystemRunner struct{}

func (d *DefaultSystemRunner) ReadFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)
}

func (d *DefaultSystemRunner) RunCmdWithContext(ctx context.Context, name string, arg ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, arg...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// getBaseDevice aliases extractBaseDevice for backward compatibility with backup.go
func getBaseDevice(device string) string {
	return extractBaseDevice(device)
}

// CPUCoreUsage содержит загрузку одного ядра CPU
type CPUCoreUsage struct {
	CoreID string
	Usage  float64
}

// getCPUUsage читает /proc/stat дважды с паузой и вычисляет загрузку по ядрам
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
			if len(fields) < 5 {
				continue
			}
			coreName := fields[0]
			var total float64
			for _, f := range fields[1:] {
				v, _ := strconv.ParseFloat(f, 64)
				total += v
			}
			idle, _ := strconv.ParseFloat(fields[4], 64)
			res[coreName] = struct{ idle, total float64 }{idle: idle, total: total}
		}
		return res, nil
	}

	stat1, err := readStats()
	if err != nil {
		return nil, err
	}
	time.Sleep(200 * time.Millisecond)
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

// MemInfo содержит информацию о RAM и Swap из /proc/meminfo
type MemInfo struct {
	TotalRAMBytes  float64
	UsedRAMBytes   float64
	RAMUsagePct    float64
	TotalSwapBytes float64
	UsedSwapBytes  float64
	SwapUsagePct   float64
}

// getMemoryInfo читает /proc/meminfo и возвращает статистику памяти
func (s *server) getMemoryInfo() (*MemInfo, error) {
	data, err := s.sysRunner.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, err
	}

	var memTotal, memAvailable, swapTotal, swapFree float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseFloat(fields[1], 64)
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
	usedSwap := swapTotal - swapFree
	return &MemInfo{
		TotalRAMBytes:  memTotal,
		UsedRAMBytes:   usedRAM,
		RAMUsagePct:    pct(usedRAM, memTotal),
		TotalSwapBytes: swapTotal,
		UsedSwapBytes:  usedSwap,
		SwapUsagePct:   pct(usedSwap, swapTotal),
	}, nil
}

func pct(used, total float64) float64 {
	if total > 0 {
		return (used / total) * 100
	}
	return 0
}

// getUptime читает /proc/uptime и возвращает форматированную строку
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

// getDiskType определяет тип диска (SSD/HDD) по точке монтирования
func getDiskType(mountPath string) string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "Unknown"
	}
	var deviceName string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == mountPath {
			deviceName = fields[0]
			break
		}
	}
	if deviceName == "" {
		return "Unknown"
	}
	deviceName = strings.TrimPrefix(deviceName, "/dev/")
	baseDevice := extractBaseDevice(deviceName)

	rotRaw, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/queue/rotational", baseDevice))
	if err != nil {
		return "Unknown"
	}
	if strings.TrimSpace(string(rotRaw)) == "0" {
		return "SSD"
	}
	return "HDD"
}

// extractBaseDevice извлекает базовое имя блочного устройства (sda1 -> sda, nvme0n1p1 -> nvme0n1)
func extractBaseDevice(device string) string {
	if strings.HasPrefix(device, "nvme") {
		if idx := strings.LastIndex(device, "p"); idx > 0 {
			if _, err := strconv.Atoi(device[idx+1:]); err == nil {
				return device[:idx]
			}
		}
		return device
	}
	for i := len(device) - 1; i >= 0; i-- {
		if device[i] < '0' || device[i] > '9' {
			return device[:i+1]
		}
	}
	return device
}

// getServerModel читает модель железа из DMI
func getServerModel() string {
	data, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil {
		log.Printf("⚠️ Не удалось прочитать модель сервера из DMI: %v", err)
		return ""
	}
	return strings.TrimSpace(string(data))
}
