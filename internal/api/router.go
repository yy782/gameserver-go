package api

import (
	"net/http"
	"time"

	"gameserver/internal/common"

	"github.com/gin-gonic/gin"
)

// Server 运营管理面 HTTP 服务器（Gin）
// 分层：Gin 仅承载 HTTP 语义（路由/绑定/中间件），业务数据全部经由 ApiService
// 向下游 gRPC 微服务与 Redis/MySQL 获取，保证与玩家 TCP 链路解耦。
type Server struct {
	svc    *ApiService
	auth   *AdminAuth
	engine *gin.Engine
	start  time.Time
}

// NewServer 创建管理面 HTTP 服务器并构建路由
func NewServer(svc *ApiService, cfg Config) *Server {
	s := &Server{
		svc:   svc,
		auth:  NewAdminAuth(cfg),
		start: time.Now(),
	}
	s.engine = s.buildRouter()
	return s
}

// Handler 返回可被 http.Server 承载的处理器
func (s *Server) Handler() http.Handler {
	return s.engine
}

// buildRouter 组装中间件与路由
func (s *Server) buildRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), s.accessLog(), cors())

	// 健康检查：运维探活用，无需登录
	r.GET("/healthz", s.handleHealthz)

	// ---- 以下均需运营登录（JWT） ----
	admin := r.Group("/api/v1")
	admin.Use(s.requireAdmin())

	admin.GET("/servers", s.handleServers)
	admin.GET("/rank/top", s.handleRankTop)
	admin.GET("/players/:id", s.handlePlayer)
	admin.GET("/me", s.handleMe)

	// 运营登录放最后注册与鉴权组隔离
	r.POST("/api/v1/admin/login", s.handleAdminLogin)

	return r
}

// accessLog 请求访问日志中间件
func (s *Server) accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		common.Info("[api] %s %d %s %s %s",
			c.ClientIP(), c.Writer.Status(), c.Request.Method,
			c.Request.RequestURI, time.Since(start).Round(time.Microsecond))
	}
}

// cors 跨域支持（运营后台 Web 前端可能独立域名部署）
func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
