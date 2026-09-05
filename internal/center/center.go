package center

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"strconv"
	"sync"
)

// 服务心跳超时时间：超过该时长未心跳的服务从注册表摘除
const heartbeatTimeoutMs = 30 * 1000

// Center 中心服务器
// 职责：
//  1. 服务注册 / 发现（内存注册表 + 心跳保活）
//  2. token 校验（查询 Redis 会话表，供网关登录闭环）
type Center struct {
	pb.UnimplementedCenterServiceServer

	redis *rpc.RedisClient

	mu       sync.RWMutex
	services map[string]*serviceEntry
}

// serviceEntry 注册表中的一条服务记录
type serviceEntry struct {
	name   string
	host   string
	port   int32
	kind   string
	lastHB int64 // 最近一次心跳时间（毫秒）
}

// NewCenter 创建中心服务
func NewCenter(redisHost string, redisPort int) *Center {
	return &Center{
		redis:    rpc.NewRedisClient(redisHost, redisPort),
		services: make(map[string]*serviceEntry),
	}
}

// VerifyToken 校验 Token
// token 由 login 签发并写入 Redis（SETEX token:{token} 600 = player_id），
// 这里查 Redis 拿到 player_id 后，再读玩家热数据缓存补全信息返回。
func (c *Center) VerifyToken(ctx context.Context, req *pb.TokenReq) (*pb.TokenRsp, error) {
	if req.Token == "" {
		return &pb.TokenRsp{Ok: false}, nil
	}

	// 1. 查会话表：token -> player_id
	pidStr, err := c.redis.Get(ctx, "token:"+req.Token)
	if err != nil || pidStr == "" {
		common.Warn("[center] token 校验失败（不存在）")
		return &pb.TokenRsp{Ok: false}, nil
	}
	playerID, err := strconv.ParseInt(pidStr, 10, 64)
	if err != nil {
		return &pb.TokenRsp{Ok: false}, nil
	}

	// 2. 读玩家热数据缓存补全玩家信息
	player, ok := c.buildPlayer(ctx, playerID)
	if !ok {
		common.Warn("[center] 玩家缓存读取失败 player_id=%d", playerID)
		return &pb.TokenRsp{Ok: false}, nil
	}
	return &pb.TokenRsp{Ok: true, Player: player}, nil
}

// buildPlayer 从 Redis 玩家热数据缓存构造 PlayerBase
func (c *Center) buildPlayer(ctx context.Context, playerID int64) (*pb.PlayerBase, bool) {
	infoKey := "player:" + strconv.FormatInt(playerID, 10) + ":info"

	name, err := c.redis.HGet(ctx, infoKey, "name")
	if err != nil || name == "" {
		return nil, false
	}
	player := &pb.PlayerBase{
		PlayerId: playerID,
		Name:     name,
	}
	if level, err := c.redis.HGet(ctx, infoKey, "level"); err == nil {
		if v, e := strconv.Atoi(level); e == nil {
			player.Level = int32(v)
		}
	}
	if score, err := c.redis.HGet(ctx, infoKey, "score"); err == nil {
		if v, e := strconv.Atoi(score); e == nil {
			player.Score = int32(v)
		}
	}
	return player, true
}

// RegisterService 服务注册（不存在则新增，已存在则覆盖并刷新心跳）
func (c *Center) RegisterService(ctx context.Context, req *pb.RegReq) (*pb.RegRsp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := common.NowMs()
	if e, ok := c.services[req.ServiceName]; ok {
		e.host = req.Host
		e.port = req.Port
		e.kind = req.Kind
		e.lastHB = now
	} else {
		c.services[req.ServiceName] = &serviceEntry{
			name:   req.ServiceName,
			host:   req.Host,
			port:   req.Port,
			kind:   req.Kind,
			lastHB: now,
		}
	}
	common.Info("[center] 服务注册: %s (%s) at %s:%d",
		req.ServiceName, req.Kind, req.Host, req.Port)
	return &pb.RegRsp{Ok: true}, nil
}

// GetServiceList 获取服务列表（同时摘除心跳超时的服务）
func (c *Center) GetServiceList(ctx context.Context, req *pb.Empty) (*pb.ServiceList, error) {
	now := common.NowMs()

	c.mu.Lock()
	for name, e := range c.services {
		if now-e.lastHB > heartbeatTimeoutMs {
			common.Warn("[center] 服务 %s 心跳超时，从注册表摘除", name)
			delete(c.services, name)
		}
	}
	entries := make([]*pb.ServiceEntry, 0, len(c.services))
	for _, e := range c.services {
		entries = append(entries, &pb.ServiceEntry{
			ServiceName: e.name,
			Host:        e.host,
			Port:        e.port,
			Kind:        e.kind,
		})
	}
	c.mu.Unlock()

	common.Info("[center] 心跳检查, 当前存活服务: %d", len(entries))
	return &pb.ServiceList{Services: entries}, nil
}

// Heartbeat 服务心跳保活
func (c *Center) Heartbeat(ctx context.Context, req *pb.HeartbeatReq) (*pb.Empty, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.services[req.ServiceName]; ok {
		e.lastHB = common.NowMs()
	}
	return &pb.Empty{}, nil
}
