package rpc

import (
	"context"
	"fmt"
	"gameserver/api/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ClusterClient 集群客户端
type ClusterClient struct {
	centerConn *grpc.ClientConn
	loginConn  *grpc.ClientConn
	gameConns  map[string]*grpc.ClientConn

	centerClient       pb.CenterServiceClient
	loginClient        pb.LoginServiceClient
	gameClients        map[string]pb.GameServiceClient
	gatewayPushClients map[int64]pb.GatewayPushServiceClient
}

// NewClusterClient 创建集群客户端
func NewClusterClient() *ClusterClient {
	return &ClusterClient{
		gameConns:          make(map[string]*grpc.ClientConn),
		gameClients:        make(map[string]pb.GameServiceClient),
		gatewayPushClients: make(map[int64]pb.GatewayPushServiceClient),
	}
}

func (cc *ClusterClient) ConnectCenter(ctx context.Context, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	cc.centerConn = conn
	cc.centerClient = pb.NewCenterServiceClient(conn)
	return nil
}

func (cc *ClusterClient) ConnectLogin(ctx context.Context, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	cc.loginConn = conn
	cc.loginClient = pb.NewLoginServiceClient(conn)
	return nil
}

func (cc *ClusterClient) ConnectGame(ctx context.Context, name, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	cc.gameConns[name] = conn
	cc.gameClients[name] = pb.NewGameServiceClient(conn)
	return nil
}

func (cc *ClusterClient) ConnectGatewayPush(ctx context.Context, playerID int64, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	gwConn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	cc.gatewayPushClients[playerID] = pb.NewGatewayPushServiceClient(gwConn)
	return nil
}

// VerifyToken 验证 Token
func (cc *ClusterClient) VerifyToken(ctx context.Context, token string) (*pb.PlayerBase, error) {
	resp, err := cc.centerClient.VerifyToken(ctx, &pb.TokenReq{Token: token})
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("token verification failed")
	}
	return resp.Player, nil
}

func (cc *ClusterClient) RegisterService(ctx context.Context, serviceName, host string, port int, kind string) error {
	resp, err := cc.centerClient.RegisterService(ctx, &pb.RegReq{
		ServiceName: serviceName,
		Host:        host,
		Port:        int32(port),
		Kind:        kind,
	})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("service registration failed: %s", resp.Reason)
	}
	return nil
}

func (cc *ClusterClient) GetServiceList(ctx context.Context) ([]*pb.ServiceEntry, error) {
	resp, err := cc.centerClient.GetServiceList(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	return resp.Services, nil
}

func (cc *ClusterClient) Heartbeat(ctx context.Context, serviceName string) error {
	_, err := cc.centerClient.Heartbeat(ctx, &pb.HeartbeatReq{ServiceName: serviceName})
	return err
}

// Authenticate 身份验证
func (cc *ClusterClient) Authenticate(ctx context.Context, account, password string) (*pb.AuthRsp, error) {
	return cc.loginClient.Authenticate(ctx, &pb.AuthReq{
		Account:  account,
		Password: password,
	})
}

func (cc *ClusterClient) Register(ctx context.Context, account, password, name string) (*pb.RegisterRsp, error) {
	return cc.loginClient.Register(ctx, &pb.RegisterReq{
		Account:  account,
		Password: password,
		Name:     name,
	})
}

// JoinMatch 加入匹配
func (cc *ClusterClient) JoinMatch(ctx context.Context, gameServer string, playerID int64, playerName, gatewayAddr string, mode int32) (*pb.MatchJoinRsp, error) {
	client, ok := cc.gameClients[gameServer]
	if !ok {
		return nil, fmt.Errorf("game server not connected: %s", gameServer)
	}
	return client.JoinMatch(ctx, &pb.MatchJoinReq{
		PlayerId:    playerID,
		PlayerName:  playerName,
		GatewayAddr: gatewayAddr,
		Mode:        mode,
	})
}

func (cc *ClusterClient) QueryMatchResult(ctx context.Context, gameServer string, playerID int64) (*pb.MatchQueryRsp, error) {
	client, ok := cc.gameClients[gameServer]
	if !ok {
		return nil, fmt.Errorf("game server not connected: %s", gameServer)
	}
	return client.QueryMatchResult(ctx, &pb.MatchQueryReq{PlayerId: playerID})
}

func (cc *ClusterClient) SubmitOp(ctx context.Context, gameServer string, roomID, playerID int64, op *pb.OpInput) (*pb.OpForwardRsp, error) {
	client, ok := cc.gameClients[gameServer]
	if !ok {
		return nil, fmt.Errorf("game server not connected: %s", gameServer)
	}
	return client.SubmitOp(ctx, &pb.OpForwardReq{
		RoomId:   roomID,
		PlayerId: playerID,
		Op:       op,
	})
}

// PushSnapshot 推送快照
func (cc *ClusterClient) PushSnapshot(ctx context.Context, playerID int64, snapshot *pb.StateSnapshot) error {
	client, ok := cc.gatewayPushClients[playerID]
	if !ok {
		return fmt.Errorf("gateway push client not connected for player %d", playerID)
	}
	_, err := client.PushSnapshot(ctx, &pb.SnapshotPushReq{
		PlayerId:  playerID,
		Snapshot: snapshot,
	})
	return err
}

func (cc *ClusterClient) PushFrame(ctx context.Context, playerID int64, frame *pb.FrameData) error {
	client, ok := cc.gatewayPushClients[playerID]
	if !ok {
		return fmt.Errorf("gateway push client not connected for player %d", playerID)
	}
	_, err := client.PushFrame(ctx, &pb.FramePushReq{
		PlayerId: playerID,
		Frame:    frame,
	})
	return err
}

func (cc *ClusterClient) PushResult(ctx context.Context, playerID int64, result *pb.BattleResult) error {
	client, ok := cc.gatewayPushClients[playerID]
	if !ok {
		return fmt.Errorf("gateway push client not connected for player %d", playerID)
	}
	_, err := client.PushResult(ctx, &pb.ResultPushReq{
		PlayerId: playerID,
		Result:   result,
	})
	return err
}

func (cc *ClusterClient) Close() error {
	if cc.centerConn != nil {
		cc.centerConn.Close()
	}
	if cc.loginConn != nil {
		cc.loginConn.Close()
	}
	for _, conn := range cc.gameConns {
		conn.Close()
	}
	for range cc.gatewayPushClients {
		// TODO: 关闭连接
	}
	return nil
}