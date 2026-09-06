package main

import (
	"log"
	"net"
	"time"

	pb "go-pet-hsagent/proto"

	"github.com/BurntSushi/toml"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

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
