package api

import (
	"strings"
	"testing"
	"time"
)

func newTestAuth(expireSec int64) *AdminAuth {
	return NewAdminAuth(Config{
		AdminAccount:  "admin",
		AdminPassword: "admin123",
		JWTSecret:     "test-secret",
		JWTExpireSec:  expireSec,
	})
}

// TestAdminLoginConfig 默认配置补齐
func TestAdminLoginConfig(t *testing.T) {
	cfg := Config{} // 全空
	a := NewAdminAuth(cfg)
	if a.cfg.AdminAccount != "admin" || a.cfg.AdminPassword != "admin123" {
		t.Errorf("默认账号密码未生效: %+v", a.cfg)
	}
	if a.cfg.JWTExpireSec != defaultTokenExpireSec {
		t.Errorf("默认过期时间未生效: %d", a.cfg.JWTExpireSec)
	}
	if !a.VerifyAccount("admin", "admin123") {
		t.Error("默认账号应可通过校验")
	}
	if a.VerifyAccount("admin", "wrong") {
		t.Error("错误密码不应通过校验")
	}
}

// TestVerifyAccount 常量时间比对
func TestVerifyAccount(t *testing.T) {
	a := newTestAuth(0)
	if !a.VerifyAccount("admin", "admin123") {
		t.Error("正确账号密码应通过")
	}
	for _, tt := range []struct {
		acct, pwd string
	}{
		{"", "admin123"},
		{"admin", ""},
		{"admin", "admin1234"},
		{"Admin", "admin123"},
	} {
		if a.VerifyAccount(tt.acct, tt.pwd) {
			t.Errorf("非法组合应失败: %q %q", tt.acct, tt.pwd)
		}
	}
}


// TestSignVerifyToken 签发与校验往返
func TestSignVerifyToken(t *testing.T) {
	a := newTestAuth(0)
	token, err := a.SignToken("admin")
	if err != nil {
		t.Fatalf("SignToken failed: %v", err)
	}
	if !strings.Contains(token, ".") {
		t.Fatalf("token 不是 JWT 格式: %s", token)
	}
	claims, err := a.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}
	if claims.Account != "admin" || claims.Role != "admin" {
		t.Errorf("claims 不完整: %+v", claims)
	}
	if !claims.ExpiresAt.Time.After(time.Now()) {
		t.Error("签发后 token 应立即有效")
	}
}

// TestVerifyTokenExpired 过期 token 校验失败
func TestVerifyTokenExpired(t *testing.T) {
	a := newTestAuth(1)
	token, err := a.SignToken("admin")
	if err != nil {
		t.Fatalf("SignToken failed: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := a.VerifyToken(token); err == nil {
		t.Error("过期 token 应校验失败")
	}
}

// TestVerifyTokenTampered 篡改签名/无效 token 校验失败
func TestVerifyTokenTampered(t *testing.T) {
	a := newTestAuth(0)
	token, _ := a.SignToken("admin")
	for _, bad := range []string{
		token + "x",            // 追加字符破坏签名
		token[:len(token)-4],   // 截断
		"not-a-jwt",
		"",
		"aaa.bbb.ccc",
	} {
		if _, err := a.VerifyToken(bad); err == nil {
			t.Errorf("非法 token 应校验失败: %q", bad)
		}
	}
	// 换密钥签发应校验失败（不同实例）
	other := newTestAuth(0)
	other.cfg.JWTSecret = "another-secret"
	otherToken, _ := other.SignToken("admin")
	if _, err := a.VerifyToken(otherToken); err == nil {
		t.Error("不同密钥签发的 token 应校验失败")
	}
}
