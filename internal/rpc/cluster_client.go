package rpc

import (
	"context"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ServiceAddr 服务地址（host:port）
type ServiceAddr struct {
	Host string
	Port int32
}

func (a ServiceAddr) String() string {
	return common.FormatAddr(a.Host, int(a.Port))
}

// ClusterClient 集群客户端：封装对 center/login/game/rank 的 gRPC 调用。
// - 各服务地址通过 config 直连（center/login）或通过中心服务发现动态刷新（game/rank）；
// - 所有底层连接按地址缓存复用（GetConn），避免重复握手。
type ClusterClient struct {
	centerClient pb.CenterServiceClient
	centerAddr   ServiceAddr
	centerConn   *grpc.ClientConn

	loginClient pb.LoginServiceClient
	loginAddr   ServiceAddr
	loginConn   *grpc.ClientConn

	rankClient pb.RankServiceClient
	rankAddr   ServiceAddr
	rankConn   *grpc.ClientConn

	mu       sync.RWMutex
	gameList []ServiceAddr // 发现的 game 实例（kind=game）
	gamePick uint32        // round-robin 游标

	connMu   sync.Mutex
	connPool map[string]*grpc.ClientConn // 动态直连缓存（按地址），用于 room:route 定向转发
}

// NewClusterClient 创建集群客户端
func NewClusterClient() *ClusterClient {
	return &ClusterClient{
		connPool: make(map[string]*grpc.ClientConn),
	}
}

// getConn 按地址获取（并缓存）一个 gRPC 连接
func (cc *ClusterClient) getConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	cc.connMu.Lock()
	defer cc.connMu.Unlock()

	if conn, ok := cc.connPool[addr]; ok && conn != nil {
		return conn, nil
	}
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	cc.connPool[addr] = conn
	return conn, nil
}

// SetCenterAddr 设置中心服地址
func (cc *ClusterClient) SetCenterAddr(addr ServiceAddr) { cc.centerAddr = addr }

// SetLoginAddr 设置登录服地址
func (cc *ClusterClient) SetLoginAddr(addr ServiceAddr) { cc.loginAddr = addr }

// SetRankAddr 设置排行榜服地址（服务发现刷新后调用）
func (cc *ClusterClient) SetRankAddr(addr ServiceAddr) {
	cc.mu.Lock()
	cc.rankAddr = addr
	cc.mu.Unlock()
}

// SetGameList 刷新 game 实例列表（服务发现）
func (cc *ClusterClient) SetGameList(addrs []ServiceAddr) {
	cc.mu.Lock()
	cc.gameList = addrs
	cc.mu.Unlock()
}

// PickGame round-robin 选取一个 game 实例；无可用实例返回 false
func (cc *ClusterClient) PickGame() (ServiceAddr, bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if len(cc.gameList) == 0 {
		return ServiceAddr{}, false
	}
	addr := cc.gameList[int(cc.gamePick)%len(cc.gameList)]
	cc.gamePick++
	return addr, true
}

// GameCount 当前已知 game 实例数量
func (cc *ClusterClient) GameCount() int {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return len(cc.gameList)
}

// centerClient 懒连接中心服
func (cc *ClusterClient) center(ctx context.Context) (pb.CenterServiceClient, error) {
	if cc.centerClient != nil {
		return cc.centerClient, nil
	}
	addr := cc.centerAddr.String()
	if addr == ":" {
		return nil, fmt.Errorf("center addr not configured")
	}
	conn, err := cc.getConn(ctx, addr)
	if err != nil {
		return nil, err
	}
	cc.centerClient = pb.NewCenterServiceClient(conn)
	cc.centerConn = conn
	return cc.centerClient, nil
}

// login 懒连接登录服
func (cc *ClusterClient) login(ctx context.Context) (pb.LoginServiceClient, error) {
	if cc.loginClient != nil {
		return cc.loginClient, nil
	}
	addr := cc.loginAddr.String()
	if addr == ":" {
		return nil, fmt.Errorf("login addr not configured")
	}
	conn, err := cc.getConn(ctx, addr)
	if err != nil {
		return nil, err
	}
	cc.loginClient = pb.NewLoginServiceClient(conn)
	cc.loginConn = conn
	return cc.loginClient, nil
}

// rank 懒连接排行榜服
func (cc *ClusterClient) rank(ctx context.Context) (pb.RankServiceClient, error) {
	cc.mu.RLock()
	addr := cc.rankAddr
	cc.mu.RUnlock()
	if addr.Port == 0 {
		return nil, fmt.Errorf("rank addr not discovered")
	}
	if cc.rankClient != nil {
		return cc.rankClient, nil
	}
	conn, err := cc.getConn(ctx, addr.String())
	if err != nil {
		return nil, err
	}
	cc.rankClient = pb.NewRankServiceClient(conn)
	cc.rankConn = conn
	return cc.rankClient, nil
}

// gameClient 按地址连接指定 game 实例（懒连接，地址可来自 room:route 或服务发现）
func (cc *ClusterClient) gameClient(ctx context.Context, addr string) (pb.GameServiceClient, error) {
	if addr == "" {
		return nil, fmt.Errorf("game addr empty")
	}
	conn, err := cc.getConn(ctx, addr)
	if err != nil {
		return nil, err
	}
	return pb.NewGameServiceClient(conn), nil
}

// ---------------- center ----------------

// RegisterService 注册本服务到中心服
func (cc *ClusterClient) RegisterService(ctx context.Context, serviceName, host string, port int, kind string) error {
	c, err := cc.center(ctx)
	if err != nil {
		return err
	}
	resp, err := c.RegisterService(ctx, &pb.RegReq{
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

// Heartbeat 向中心服发送心跳
func (cc *ClusterClient) Heartbeat(ctx context.Context, serviceName string) error {
	c, err := cc.center(ctx)
	if err != nil {
		return err
	}
	_, err = c.Heartbeat(ctx, &pb.HeartbeatReq{ServiceName: serviceName})
	return err
}

// GetServiceList 获取中心服存活服务列表
func (cc *ClusterClient) GetServiceList(ctx context.Context) ([]*pb.ServiceEntry, error) {
	c, err := cc.center(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.GetServiceList(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	return resp.Services, nil
}

// VerifyToken 校验 token 并取回玩家信息
func (cc *ClusterClient) VerifyToken(ctx context.Context, token string) (*pb.PlayerBase, error) {
	c, err := cc.center(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.VerifyToken(ctx, &pb.TokenReq{Token: token})
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("token verification failed")
	}
	return resp.Player, nil
}

// ---------------- login ----------------

// Authenticate 登录认证
func (cc *ClusterClient) Authenticate(ctx context.Context, account, password string) (*pb.AuthRsp, error) {
	l, err := cc.login(ctx)
	if err != nil {
		return nil, err
	}
	return l.Authenticate(ctx, &pb.AuthReq{Account: account, Password: password})
}

// Register 注册账号
func (cc *ClusterClient) Register(ctx context.Context, account, password, name string) (*pb.RegisterAccountRsp, error) {
	l, err := cc.login(ctx)
	if err != nil {
		return nil, err
	}
	return l.Register(ctx, &pb.RegisterAccountReq{Account: account, Password: password, Name: name})
}

// ---------------- rank ----------------

// SubmitScore 提交战斗分数（排行榜服务做 ZINCRBY，只增不减）
func (cc *ClusterClient) SubmitScore(ctx context.Context, playerID int64, score int32) (int32, error) {
	r, err := cc.rank(ctx)
	if err != nil {
		return -1, err
	}
	resp, err := r.SubmitScore(ctx, &pb.ScoreReq{PlayerId: playerID, Score: score})
	if err != nil {
		return -1, err
	}
	if !resp.Ok {
		return -1, fmt.Errorf("submit score failed")
	}
	return resp.Rank, nil
}

// GetTopN 获取排行榜 TopN
func (cc *ClusterClient) GetTopN(ctx context.Context, n int32) (*pb.TopNRsp, error) {
	r, err := cc.rank(ctx)
	if err != nil {
		return nil, err
	}
	return r.GetTopN(ctx, &pb.TopNReq{N: n})
}

// ---------------- game ----------------

// JoinMatch 向指定 game 实例发起匹配入队（返回 room_id=0 表示排队中）
func (cc *ClusterClient) JoinMatch(ctx context.Context, addr ServiceAddr, playerID int64, playerName, gatewayAddr string, mode int32) (*pb.MatchJoinRsp, error) {
	g, err := cc.gameClient(ctx, addr.String())
	if err != nil {
		return nil, err
	}
	return g.JoinMatch(ctx, &pb.MatchJoinReq{
		PlayerId:    playerID,
		PlayerName:  playerName,
		GatewayAddr: gatewayAddr,
		Mode:        mode,
	})
}

// QueryMatchResult 轮询某 game 实例查询匹配结果
func (cc *ClusterClient) QueryMatchResult(ctx context.Context, addr ServiceAddr, playerID int64) (*pb.MatchQueryRsp, error) {
	g, err := cc.gameClient(ctx, addr.String())
	if err != nil {
		return nil, err
	}
	return g.QueryMatchResult(ctx, &pb.MatchQueryReq{PlayerId: playerID})
}

// SubmitOpTo 向房间所在 game 实例定向转发操作（room:route 路由）
func (cc *ClusterClient) SubmitOpTo(ctx context.Context, addr string, roomID, playerID int64, op *pb.OpInput) (*pb.OpForwardRsp, error) {
	g, err := cc.gameClient(ctx, addr)
	if err != nil {
		return nil, err
	}
	return g.SubmitOp(ctx, &pb.OpForwardReq{
		RoomId:   roomID,
		PlayerId: playerID,
		Op:       op,
	})
}

// QuitRoomTo 通知房间所在 game 实例玩家退出
func (cc *ClusterClient) QuitRoomTo(ctx context.Context, addr string, roomID, playerID int64) error {
	g, err := cc.gameClient(ctx, addr)
	if err != nil {
		return err
	}
	_, err = g.QuitRoom(ctx, &pb.QuitRoomReq{RoomId: roomID, PlayerId: playerID})
	return err
}

// BroadcastLeaveMatch 玩家匹配中下线时，向所有 game 实例广播移出匹配池（幂等）
func (cc *ClusterClient) BroadcastLeaveMatch(ctx context.Context, playerID int64, playerName, gatewayAddr string) {
	cc.mu.RLock()
	addrs := append([]ServiceAddr(nil), cc.gameList...)
	cc.mu.RUnlock()
	for _, addr := range addrs {
		g, err := cc.gameClient(ctx, addr.String())
		if err != nil {
			continue
		}
		_, _ = g.LeaveMatch(ctx, &pb.LeaveMatchReq{
			PlayerId:    playerID,
			PlayerName:  playerName,
			GatewayAddr: gatewayAddr,
		})
	}
}

// Close 关闭全部连接
func (cc *ClusterClient) Close() error {
	cc.connMu.Lock()
	defer cc.connMu.Unlock()
	var firstErr error
	for addr, conn := range cc.connPool {
		if conn != nil {
			if err := conn.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(cc.connPool, addr)
	}
	return firstErr
}

// GetTimeoutCtx 便捷方法：带超时的 context
func GetTimeoutCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
