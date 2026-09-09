package main

import (
	"fmt"
	pb "go-pet-hsagent/proto/hsagent/v1"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure" // Новый импорт для нешифрованного соединения
	"google.golang.org/grpc/keepalive"
)

var kacp = keepalive.ClientParameters{
	Time:                1 * time.Minute, // Пингуем раз в минуту
	Timeout:             5 * time.Second, // Ждем ответ 5 секунд
	PermitWithoutStream: true,            // Пингуем даже при отсутствии активных стримов
}

func initGRPCClient() (pb.MonitorServiceClient, *grpc.ClientConn, error) {
	// Использование актуальной функции grpc.NewClient
	conn, err := grpc.NewClient(
		agentAddr, // Или адрес/сокет хоста для CasaOS
		grpc.WithTransportCredentials(insecure.NewCredentials()), // Замена WithInsecure()
		grpc.WithKeepaliveParams(kacp),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create grpc client: %w", err)
	}

	client := pb.NewMonitorServiceClient(conn)
	return client, conn, nil
}
