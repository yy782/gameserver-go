package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"gameserver/internal/common"
	"gameserver/internal/rpc"

	"github.com/gin-gonic/gin"
)

// ---------- 健康检查 ----------

// handleHealthz 进程存活 + 关键依赖（Redis / 中心服）探活。
// 供 k8s/云监控/运维脚本探测，不要求登录。
func (s *Server) handleHealthz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	redisOK := s.svc.RedisOK(ctx)
	centerOK := s.svc.CenterOK(ctx)

	status := http.StatusOK
	code := 0
	msg := "ok"
	if !redisOK || !centerOK {
		status = http.StatusServiceUnavailable
		code = http.StatusServiceUnavailable
		msg = "依赖服务异常"
	}
	c.JSON(status, Resp{Code: code, Msg: msg, Data: gin.H{
		"service":    s.svc.name,
		"uptime_sec": int64(time.Since(s.start).Seconds()),
		"redis":      redisOK,
		"center":     centerOK,
	}})
}

// ---------- 服务拓扑 ----------

type serviceEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int32  `json:"port"`
}

// handleServers 从中心服拉取实时服务注册表，按 kind 分组返回集群拓扑。
func (s *Server) handleServers(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	services, err := s.svc.cc.GetServiceList(ctx)
	if err != nil {
		writeErr(c, http.StatusBadGateway, http.StatusBadGateway, "中心服不可用，请稍后重试")
		return
	}

	grouped := make(map[string][]serviceEntry)
	for _, e := range services {
		grouped[e.Kind] = append(grouped[e.Kind], serviceEntry{
			Name: e.ServiceName,
			Host: e.Host,
			Port: e.Port,
		})
	}

	// kind 按固定顺序输出，便于阅读；未知 kind 放末尾
	order := []string{"center", "login", "gateway", "game", "rank", "api"}
	seen := make(map[string]bool)
	out := make([]gin.H, 0, len(grouped))
	for _, kind := range order {
		if entries, ok := grouped[kind]; ok {
			out = append(out, gin.H{"kind": kind, "count": len(entries), "instances": entries})
			seen[kind] = true
		}
	}
	var rest []string
	for kind := range grouped {
		if !seen[kind] {
			rest = append(rest, kind)
		}
	}
	sort.Strings(rest)
	for _, kind := range rest {
		entries := grouped[kind]
		out = append(out, gin.H{"kind": kind, "count": len(entries), "instances": entries})
	}

	writeOK(c, gin.H{
		"total":     len(services),
		"generated": common.NowMs(),
		"groups":    out,
	})
}

// ---------- 排行榜 ----------

type rankEntry struct {
	Rank     int32  `json:"rank"`
	PlayerID int64  `json:"player_id"`
	Name     string `json:"name"`
	Level    int32  `json:"level"`
	Score    int32  `json:"score"`
}

// handleRankTop 实时排行榜 TopN。
// GET /api/v1/rank/top?n=20   n 取值 1~100，默认 10
func (s *Server) handleRankTop(c *gin.Context) {
	n := int64(10)
	if v := c.Query("n"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 32); err == nil {
			n = parsed
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 100 {
		n = 100
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	top, err := s.svc.cc.GetTopN(ctx, int32(n))
	if err != nil {
		common.Error("[api] 拉取排行失败: %v", err)
		writeErr(c, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "排行服务未就绪")
		return
	}

	list := make([]rankEntry, 0, len(top.Players))
	for i, p := range top.Players {
		list = append(list, rankEntry{
			Rank:     int32(i + 1),
			PlayerID: p.PlayerId,
			Name:     p.Name,
			Level:    p.Level,
			Score:    p.Score,
		})
	}
	writeOK(c, gin.H{"count": len(list), "list": list})
}

// ---------- 玩家查询 ----------

// playerProfile 玩家档案视图（不暴露密码哈希/盐等敏感列）
type playerProfile struct {
	PlayerID int64  `json:"player_id"`
	Account  string `json:"account"`
	Name     string `json:"name"`
	Level    int32  `json:"level"`
	Exp      int64  `json:"exp"`
	Gold     int64  `json:"gold"`
	Score    int32  `json:"score"`
	ServerID int32  `json:"server_id"`
}

// handlePlayer 查询玩家完整档案（MySQL 玩家表）。
// GET /api/v1/players/:id
func (s *Server) handlePlayer(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(c, http.StatusBadRequest, http.StatusBadRequest, "玩家 ID 不合法")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	player, err := s.svc.mysql.GetPlayerByID(ctx, id)
	if err != nil {
		writeErr(c, http.StatusNotFound, http.StatusNotFound, "玩家不存在")
		return
	}
	writeOK(c, fromPlayer(player))
}

func fromPlayer(p *rpc.Player) playerProfile {
	return playerProfile{
		PlayerID: p.ID,
		Account:  p.Account,
		Name:     p.Name,
		Level:    p.Level,
		Exp:      p.Exp,
		Gold:     p.Gold,
		Score:    p.Score,
		ServerID: p.ServerID,
	}
}

// ---------- 当前运营账号 ----------

// handleMe 返回当前登录的运营账号信息（由 JWT 中间件注入）
func (s *Server) handleMe(c *gin.Context) {
	writeOK(c, gin.H{
		"account": c.GetString("account"),
		"role":    c.GetString("role"),
	})
}
