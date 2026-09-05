package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gameserver/internal/common"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// 默认运营 token 有效期（秒）
const defaultTokenExpireSec = 7200

// adminClaims 运营后台登录态的自定义 JWT 载荷
type adminClaims struct {
	Account string `json:"account"`
	Role    string `json:"role"` // 预留角色字段，便于后续做细粒度权限
	jwt.RegisteredClaims
}

// AdminAuth 运营后台账号校验与 JWT 签发/校验
type AdminAuth struct {
	cfg Config
}

// NewAdminAuth 创建运营鉴权器
func NewAdminAuth(cfg Config) *AdminAuth {
	if cfg.AdminAccount == "" {
		cfg.AdminAccount = "admin"
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = "admin123"
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "gameserver-admin-default-secret"
	}
	if cfg.JWTExpireSec <= 0 {
		cfg.JWTExpireSec = defaultTokenExpireSec
	}
	return &AdminAuth{cfg: cfg}
}

// VerifyAccount 常量时间比较账号密码，避免时序侧信道
func (a *AdminAuth) VerifyAccount(account, password string) bool {
	u := subtle.ConstantTimeCompare([]byte(account), []byte(a.cfg.AdminAccount))
	p := subtle.ConstantTimeCompare([]byte(password), []byte(a.cfg.AdminPassword))
	return u == 1 && p == 1
}

// SignToken 为运营账号签发 HS256 JWT
func (a *AdminAuth) SignToken(account string) (string, error) {
	now := time.Now()
	claims := adminClaims{
		Account: account,
		Role:    "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "gameserver-api",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(a.cfg.JWTExpireSec) * time.Second)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.cfg.JWTSecret))
}

// VerifyToken 校验 JWT 并返回载荷；无效/过期返回错误
func (a *AdminAuth) VerifyToken(tokenStr string) (*adminClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &adminClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(a.cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*adminClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// requireAdmin JWT 鉴权中间件：从 Authorization: Bearer <token> 提取并校验，
// 通过后把运营账号写入 gin.Context 供后续 handler 使用。
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			writeErr(c, http.StatusUnauthorized, http.StatusUnauthorized, "缺少访问凭证，请先登录")
			return
		}
		claims, err := s.auth.VerifyToken(strings.TrimSpace(token))
		if err != nil {
			common.Warn("[api] 运营 token 校验失败: %v", err)
			writeErr(c, http.StatusUnauthorized, http.StatusUnauthorized, "登录已失效，请重新登录")
			return
		}
		c.Set("account", claims.Account)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// handleAdminLogin 运营后台登录：账号密码匹配后签发 JWT
// POST /api/v1/admin/login  body: {"account":"admin","password":"admin123"}
func (s *Server) handleAdminLogin(c *gin.Context) {
	var req struct {
		Account  string `json:"account" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeErr(c, http.StatusBadRequest, http.StatusBadRequest, "参数错误："+err.Error())
		return
	}

	if !s.auth.VerifyAccount(req.Account, req.Password) {
		common.Warn("[api] 运营登录失败: account=%s", req.Account)
		writeErr(c, http.StatusUnauthorized, http.StatusUnauthorized, "账号或密码错误")
		return
	}

	token, err := s.auth.SignToken(req.Account)
	if err != nil {
		common.Error("[api] 运营登录签发 token 失败: %v", err)
		writeErr(c, http.StatusInternalServerError, http.StatusInternalServerError, "签发凭证失败")
		return
	}

	common.Info("[api] 运营登录成功: account=%s", req.Account)
	writeOK(c, gin.H{
		"account":    req.Account,
		"token":      token,
		"token_type": "Bearer",
		"expire_sec": s.auth.cfg.JWTExpireSec,
	})
}
