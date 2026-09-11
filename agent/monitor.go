package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"
)

func (s *server) getBatteryInfo() (int, string, error) {
	capRaw, err := s.sysRunner.ReadFile("/sys/class/power_supply/BAT0/capacity")
	if err != nil {
		return 0, "", err
	}
	statRaw, err := s.sysRunner.ReadFile("/sys/class/power_supply/BAT0/status")
	if err != nil {
		return 0, "", err
	}
	capacity, _ := strconv.Atoi(strings.TrimSpace(string(capRaw)))
	status := strings.TrimSpace(string(statRaw))
	return capacity, status, nil
}

func (s *server) getCpuTemperature() (float64, error) {
	tempRaw, err := s.sysRunner.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}
	tempMillidegrees, err := strconv.Atoi(strings.TrimSpace(string(tempRaw)))
	if err != nil {
		return 0, err
	}
	return float64(tempMillidegrees) / 1000.0, nil
}

func (s *server) getDiskUsage(path string) (float64, string, error) {
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

func (s *server) monitorBatteryLoop() {
	for {
		interval := time.Duration(s.cfg.Monitoring.BatteryIntervalMin) * time.Minute
		if interval <= 0 {
			interval = 5 * time.Minute
		}

		cap, stat, err := s.getBatteryInfo()
		if err == nil {
			s.mu.Lock()
			if s.lastCap == 0 {
				s.lastCap = cap
				s.lastStat = stat
			}

			// Оповещаем если заряд упал до 20% или 10%
			if (cap == 20 || cap == 10) && s.lastCap != cap {
				s.eventChan <- &pb.StreamEventsResponse{
					Type:    "battery",
					Message: fmt.Sprintf("🪫 Низкий заряд батареи: %d%% (%s)", cap, stat),
				}
			}
			// Оповещаем при смене статуса подключения зарядки
			if stat != s.lastStat {
				msg := "🔌 Зарядное устройство подключено."
				if stat == "Discharging" {
					msg = "🔋 Ноутбук перешел на работу от батареи."
				}
				s.eventChan <- &pb.StreamEventsResponse{Type: "battery", Message: msg}
				s.lastStat = stat
			}

			s.lastCap = cap
			s.mu.Unlock()
		}
		time.Sleep(interval)
	}
}

func (s *server) monitorCpuLoop() {
	for {
		interval := time.Duration(s.cfg.Monitoring.CpuIntervalMin) * time.Minute
		if interval <= 0 {
			interval = 2 * time.Minute
		}

		temp, err := s.getCpuTemperature()
		if err == nil {
			s.mu.Lock()
			if temp > s.cfg.Monitoring.CpuTempThreshold {
				if !s.cpuAlerted {
					s.eventChan <- &pb.StreamEventsResponse{
						Type:    "cpu",
						Message: fmt.Sprintf("🔥 Перегрев процессора! Температура: %.1f°C (Порог: %.1f°C)", temp, s.cfg.Monitoring.CpuTempThreshold),
					}
					s.cpuAlerted = true
				}
			} else {
				if s.cpuAlerted && temp < s.cfg.Monitoring.CpuTempThreshold-5.0 {
					s.eventChan <- &pb.StreamEventsResponse{
						Type:    "cpu",
						Message: fmt.Sprintf("✅ Температура процессора в норме: %.1f°C", temp),
					}
					s.cpuAlerted = false
				}
			}
			s.mu.Unlock()
		}
		time.Sleep(interval)
	}
}

func (s *server) monitorDisksLoop() {
	if s.diskAlerted == nil {
		s.diskAlerted = make(map[string]bool)
	}

	for {
		interval := time.Duration(s.cfg.Monitoring.DiskIntervalMin) * time.Minute
		if interval <= 0 {
			interval = 10 * time.Minute
		}

		paths := map[string]string{
			"System SSD":      "/",
			"Immich Storages": s.cfg.Backup.SourceDir,
		}

		for name, path := range paths {
			pct, report, err := s.getDiskUsage(path)
			if err == nil {
				s.mu.Lock()
				isAlerted := s.diskAlerted[name]
				if pct < s.cfg.Monitoring.DiskMinFreePercent {
					if !isAlerted {
						s.eventChan <- &pb.StreamEventsResponse{
							Type:    "disk",
							Message: fmt.Sprintf("⚠️ Мало места на диске [%s] (%s): %s", name, path, report),
						}
						s.diskAlerted[name] = true
					}
				} else {
					if isAlerted && pct > s.cfg.Monitoring.DiskMinFreePercent+5.0 {
						s.eventChan <- &pb.StreamEventsResponse{
							Type:    "disk",
							Message: fmt.Sprintf("✅ Место на диске [%s] восстановилось: %s", name, report),
						}
						s.diskAlerted[name] = false
					}
				}
				s.mu.Unlock()
			}
		}
		time.Sleep(interval)
	}
}
