package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/BurntSushi/toml"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var (
	version   = "unknown" // Переопределяется через -ldflags
	gitCommit = "unknown" // Переопределяется через -ldflags
	buildDate = "unknown" // Переопределяется через -ldflags
)

func main() {
	// Поддержка аргумента --version / -v
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("hsagent version: %s (commit: %s, built at: %s)\n", version, gitCommit, buildDate)
		os.Exit(0)
	}

	var cfg config
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
	srv := &server{
		cfg:         &cfg,
		eventChan:   make(chan *pb.StreamEventsResponse, 20),
		diskAlerted: make(map[string]bool),
		sysRunner:   &DefaultSystemRunner{}, // Передаем дефолтную реализацию
	}
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
