package gateway

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"sync"
	"time"
)

// GatewayPushService 网关推送服务
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

// StartGRPCServer 启动 gRPC 服务器
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

	return nil
}

// ConnectToServices 连接到其他服务
func (gw *Gateway) ConnectToServices(centerHost string, centerPort int, loginHost string, loginPort int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 连接到中心服
	centerAddr := common.FormatAddr(centerHost, centerPort)
	centerConn, err := grpc.DialContext(ctx, centerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}

	// 连接到登录服
	loginAddr := common.FormatAddr(loginHost, loginPort)
	loginConn, err := grpc.DialContext(ctx, loginAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		centerConn.Close()
		return err
	}

	gw.centerConn = centerConn
	gw.loginConn = loginConn

	return nil
}

// GatewayExtended 网关扩展
type GatewayExtended struct {
	centerConn *grpc.ClientConn
	loginConn  *grpc.ClientConn
	gameConns  map[string]*grpc.ClientConn
	connMu     sync.RWMutex
}