package gateway

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"google.golang.org/grpc"
	"net"
)

// GatewayPushService 网关推送服务
// game 服务通过该 gRPC 服务向玩家推送状态快照 / 输入帧 / 对局结果
type GatewayPushService struct {
	pb.UnimplementedGatewayPushServiceServer
	gw *Gateway
}

// NewGatewayPushService 创建网关推送服务
func NewGatewayPushService(gw *Gateway) *GatewayPushService {
	return &GatewayPushService{gw: gw}
}

func (gps *GatewayPushService) PushSnapshot(ctx context.Context, req *pb.SnapshotPushReq) (*pb.Empty, error) {
	gps.gw.PushSnapshot(req.PlayerId, req.Snapshot)
	return &pb.Empty{}, nil
}

func (gps *GatewayPushService) PushFrame(ctx context.Context, req *pb.FramePushReq) (*pb.Empty, error) {
	gps.gw.PushFrame(req.PlayerId, req.Frame)
	return &pb.Empty{}, nil
}

func (gps *GatewayPushService) PushResult(ctx context.Context, req *pb.ResultPushReq) (*pb.Empty, error) {
	gps.gw.PushResult(req.PlayerId, req.Result)
	return &pb.Empty{}, nil
}

// StartGRPCServer 启动网关推送 gRPC 服务器（供 game 服务回推数据）
func (gw *Gateway) StartGRPCServer(listenIP string, grpcPort int) error {
	lis, err := net.Listen("tcp", common.FormatAddr(listenIP, grpcPort))
	if err != nil {
		return err
	}

	server := grpc.NewServer()
	pb.RegisterGatewayPushServiceServer(server, NewGatewayPushService(gw))

	go func() {
		if err := server.Serve(lis); err != nil {
			common.Error("gRPC server error: %v", err)
		}
	}()

	common.Info("[gateway] 推送 gRPC 服务已启动: %s:%d", listenIP, grpcPort)
	return nil
}
