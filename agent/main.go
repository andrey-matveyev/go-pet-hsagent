package main

import (
	"context"
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

	lis, err := net.Listen("tcp", cfg.Listener.ListenAddress)
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
	/*
		// Отключаем USB-порт бэкапа при старте агента (в т.ч. после перезагрузки ОС),
		// чтобы он оставался выключенным. Остальные USB-порты не затрагиваются.
		// Делаем задержку, чтобы система успела инициализировать USB-стек
		// и включить питание порта, прежде чем мы его отключим.
		time.Sleep(1 * time.Minute)

		pciAddr := strings.TrimSpace(srv.cfg.Backup.TargetXhciPCI)
		if pciAddr != "" {
			var lastErr error
			var lastOut string
			for i := 0; i < 3; i++ {
				out, err := srv.setXHCIBind(pciAddr, false, context.Background())
				if err == nil {
					log.Printf("✅ USB порт бэкапа (PCI %s) отключён. Останется выключенным до повторного включения.", pciAddr)
					break
				}
				lastErr = err
				lastOut = out
				log.Printf("⚠️ Попытка %d/3 отключить USB порт бэкапа (PCI %s): %v, output: %s", i+1, pciAddr, err, out)
				time.Sleep(1 * time.Second)
			}
			if lastErr != nil {
				log.Printf("❌ Не удалось отключить USB порт бэкапа (PCI %s) после 3 попыток: %v, output: %s", pciAddr, lastErr, lastOut)
			}
		} else {
			log.Println("⚠️ backup.target_xhci_pci не задан в конфиге — пропускаем отключение USB порта бэкапа.")
		}

		out, err := srv.setXHCIBind(pciAddr, false, context.Background())
		if err == nil {
			log.Printf("✅ USB порт бэкапа (PCI %s) отключён. Останется выключенным до повторного включения.", pciAddr)
			break
		}
	*/
	gCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	if _, err := srv.setXHCIBind(srv.cfg.Backup.TargetXhciPCI, false, gCtx); err != nil {
		log.Printf("⚠️ Не удалось отключить USB порт бэкапа (PCI %s): %v", srv.cfg.Backup.TargetXhciPCI, err)
		log.Printf("Config: %+v", srv.cfg)
	} else {
		log.Printf("✅ USB порт бэкапа (PCI %s) отключён. Останется выключенным до повторного включения.", srv.cfg.Backup.TargetXhciPCI)
	}

	go srv.monitorBatteryLoop()
	go srv.monitorCpuLoop()
	go srv.monitorDisksLoop()
	go srv.monitorRebootLoop()
	go srv.startSchedulerLoop()
	log.Printf("🚀 Системный gRPC Агент успешно запущен на %s", cfg.Listener.ListenAddress)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
