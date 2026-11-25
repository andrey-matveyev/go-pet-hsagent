package core

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "go-pet-hsagent/proto/hsagent/v1"
)

var (
	TgBotToken string
	ChatID     int64
	// For local tests use 127.0.0.1
	// For CasaOS change this address to host.docker.internal:50051
	agentAddr = "host.docker.internal:50051"
)

func InitGRPCClient() (pb.MonitorServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.Dial(agentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial agent: %w", err)
	}

	client := pb.NewMonitorServiceClient(conn)
	return client, conn, nil
}
