package main

import (
	"context"
	"fmt"
	"log"
	"sync"

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
}

// gRPC Метод: Получение системного статуса
func (s *server) GetBatteryStatus(ctx context.Context, in *pb.GetBatteryStatusRequest) (*pb.GetBatteryStatusResponse, error) {
	bCap, bStat, _ := getBatteryInfo()
	cpuTemp, _ := getCpuTemperature()
	_, sysDiskReport, _ := getDiskUsage("/")
	_, storageDiskReport, _ := getDiskUsage(s.cfg.Backup.SourceDir)

	diskReport := fmt.Sprintf("💻 Системный SSD: %s\n📸 Хранилище Immich: %s", sysDiskReport, storageDiskReport)

	return &pb.GetBatteryStatusResponse{
		BatteryCapacity: int32(bCap),
		BatteryStatus:   bStat,
		CpuTemperature:  cpuTemp,
		DiskUsageInfo:   diskReport,
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
