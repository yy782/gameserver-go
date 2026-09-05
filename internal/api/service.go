package api

import (
	"context"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"time"
)

// Config 管理面 HTTP 服务配置
type Config struct {
	AdminAccount  string // 运营后台登录账号
	AdminPassword string // 运营后台登录密码（仅本服务内比对，不落库）
	JWTSecret     string // JWT 签名密钥
	JWTExpireSec  int64  // 运营 token 有效期（秒）
}

// ApiService 运营管理面服务：
// 作为 HTTP 层，向下游微服务（center/login/rank）发起 gRPC 调用，
// 并直连 Redis/MySQL 读取玩家热数据与档案，供运营后台查询。
type ApiService struct {
	name  string
	host  string // 本服务注册到中心服用的通告地址
	port  int    // HTTP 端口（注册表展示用）
	redis *rpc.RedisClient
	mysql *rpc.MySQLClient
	cc    *rpc.ClusterClient

	centerAddr rpc.ServiceAddr

	stop chan struct{}
}

// NewApiService 创建管理面服务
func NewApiService(name, host string, port int, redis *rpc.RedisClient, mysql *rpc.MySQLClient, cc *rpc.ClusterClient, centerAddr rpc.ServiceAddr) *ApiService {
	return &ApiService{
		name:       name,
		host:       host,
		port:       port,
		redis:      redis,
		mysql:      mysql,
		cc:         cc,
		centerAddr: centerAddr,
		stop:       make(chan struct{}),
	}
}

// Start 启动后台任务：注册到中心服并心跳 + 周期刷新 rank 地址（服务发现）
func (s *ApiService) Start(ctx context.Context) {
	go s.registerAndDiscoverLoop(ctx)
}

// Stop 停止后台任务
func (s *ApiService) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

// registerAndDiscoverLoop 向 center 注册本服务（kind=api）并 5s 心跳，
// 同时从中心服发现 rank 实例地址，供排行榜接口路由。
func (s *ApiService) registerAndDiscoverLoop(ctx context.Context) {
	registered := false
	for i := 0; i < 10; i++ {
		if err := s.cc.RegisterService(ctx, s.name, s.host, s.port, "api"); err == nil {
			registered = true
			break
		}
		common.Warn("[api] 中心服注册失败（第 %d 次），500ms 后重试", i+1)
		select {
		case <-time.After(500 * time.Millisecond):
		case <-s.stop:
			return
		}
	}
	if !registered {
		common.Error("[api] 中心服注册失败（重试 10 次仍失败）")
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.cc.Heartbeat(ctx, s.name); err != nil {
				common.Warn("[api] 中心服心跳失败，尝试重新注册")
				_ = s.cc.RegisterService(ctx, s.name, s.host, s.port, "api")
			}
			s.refreshPeers(ctx)
		case <-s.stop:
			return
		}
	}
}

// refreshPeers 从中心服拉取服务列表，记录 rank 实例地址
func (s *ApiService) refreshPeers(ctx context.Context) {
	services, err := s.cc.GetServiceList(ctx)
	if err != nil {
		return
	}
	for _, e := range services {
		if e.Kind == "rank" {
			s.cc.SetRankAddr(rpc.ServiceAddr{Host: e.Host, Port: e.Port})
		}
	}
}

// RedisOK 检查 Redis 连通性
func (s *ApiService) RedisOK(ctx context.Context) bool {
	return s.redis != nil && s.redis.Ping(ctx)
}

// CenterOK 探活中心服：能拉到服务列表即视为可用
func (s *ApiService) CenterOK(ctx context.Context) bool {
	if s.cc == nil {
		return false
	}
	_, err := s.cc.GetServiceList(ctx)
	return err == nil
}
