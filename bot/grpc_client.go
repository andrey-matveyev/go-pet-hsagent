package main

import (
	pb "go-pet-hsagent/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func initGRPCClient() (pb.MonitorServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.Dial(agentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	client := pb.NewMonitorServiceClient(conn)
	return client, conn, nil
}
