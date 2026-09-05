package center

import (
	"context"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"sync"
	"time"
)

// Center 中心服务器
type Center struct {
	pb.UnimplementedCenterServiceServer

	redis  *rpc.RedisClient
	mu     sync.RWMutex
	tokens map[string]*tokenInfo
}

// tokenInfo Token 信息
type tokenInfo struct {
	playerID    int64
	expireAt    int64
	serviceName string
}

// ServiceRegistry 服务注册表
type ServiceRegistry struct {
	mu       sync.RWMutex
	services map[string]*pb.ServiceEntry
}

// NewCenter 创建中心服务
func NewCenter(redisHost string, redisPort int) *Center {
	redisClient := rpc.NewRedisClient(redisHost, redisPort)
	return &Center{
		redis:  redisClient,
		tokens: make(map[string]*tokenInfo),
	}
}

// VerifyToken 验证 Token
func (c *Center) VerifyToken(ctx context.Context, req *pb.TokenReq) (*pb.TokenRsp, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	info, ok := c.tokens[req.Token]
	if !ok || info.expireAt < common.NowMs() {
		return &pb.TokenRsp{Ok: false}, nil
	}

	return &pb.TokenRsp{
		Ok: true,
		Player: &pb.PlayerBase{
			PlayerId: info.playerID,
		},
	}, nil
}

// RegisterService 服务注册
func (c *Center) RegisterService(ctx context.Context, req *pb.RegReq) (*pb.RegRsp, error) {
	// TODO: 实现服务注册逻辑
	common.Info("[center] RegisterService: %s %s:%d", req.ServiceName, req.Host, req.Port)
	return &pb.RegRsp{Ok: true}, nil
}

// GetServiceList 获取服务列表
func (c *Center) GetServiceList(ctx context.Context, req *pb.Empty) (*pb.ServiceList, error) {
	// TODO: 实现服务发现逻辑
	return &pb.ServiceList{}, nil
}

// Heartbeat 服务心跳
func (c *Center) Heartbeat(ctx context.Context, req *pb.HeartbeatReq) (*pb.Empty, error) {
	common.Info("[center] Heartbeat: %s", req.ServiceName)
	return &pb.Empty{}, nil
}

func (c *Center) StoreToken(token string, playerID int64, expireSec int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[token] = &tokenInfo{
		playerID: playerID,
		expireAt: common.NowMs() + expireSec*1000,
	}
}