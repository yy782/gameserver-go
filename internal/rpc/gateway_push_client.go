package rpc

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"sync"
	"time"
)

type GatewayPushClient struct {
	addrs map[string]*grpc.ClientConn
	mu    sync.RWMutex
}

func NewGatewayPushClient() *GatewayPushClient {
	return &GatewayPushClient{
		addrs: make(map[string]*grpc.ClientConn),
	}
}

func (g *GatewayPushClient) getConn(addr string) (*grpc.ClientConn, error) {
	g.mu.RLock()
	conn, ok := g.addrs[addr]
	g.mu.RUnlock()

	if ok && conn != nil {
		return conn, nil
	}

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	g.mu.Lock()
	g.addrs[addr] = conn
	g.mu.Unlock()

	return conn, nil
}

func (g *GatewayPushClient) PushSnapshot(gatewayAddr string, playerID int64, snapshot *pb.StateSnapshot) bool {
	conn, err := g.getConn(gatewayAddr)
	if err != nil {
		common.Error("Failed to dial gateway %s: %v", gatewayAddr, err)
		return false
	}

	client := pb.NewGatewayPushServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.PushSnapshot(ctx, &pb.SnapshotPushReq{
		PlayerId: playerID,
		Snapshot: snapshot,
	})

	return err == nil
}

func (g *GatewayPushClient) PushFrame(gatewayAddr string, playerID int64, frame *pb.FrameData) bool {
	conn, err := g.getConn(gatewayAddr)
	if err != nil {
		common.Error("Failed to dial gateway %s: %v", gatewayAddr, err)
		return false
	}

	client := pb.NewGatewayPushServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.PushFrame(ctx, &pb.FramePushReq{
		PlayerId: playerID,
		Frame:    frame,
	})

	return err == nil
}

func (g *GatewayPushClient) PushResult(gatewayAddr string, playerID int64, result *pb.BattleResult) bool {
	conn, err := g.getConn(gatewayAddr)
	if err != nil {
		common.Error("Failed to dial gateway %s: %v", gatewayAddr, err)
		return false
	}

	client := pb.NewGatewayPushServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.PushResult(ctx, &pb.ResultPushReq{
		PlayerId: playerID,
		Result:   result,
	})

	return err == nil
}

func (g *GatewayPushClient) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, conn := range g.addrs {
		if conn != nil {
			conn.Close()
		}
	}
	g.addrs = make(map[string]*grpc.ClientConn)
}