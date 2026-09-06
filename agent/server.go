package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	pb "go-pet-hsagent/proto"
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

// gRPC Метод: Получение системного статуса
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
		Type:    "test",
		Message: "🔔 Тестовое уведомление: gRPC-стриминг работает корректно!",
	}

	return &pb.TestResponse{
		ResultMessage: "Тестовое событие успешно отправлено в стрим.",
	}, nil
}
