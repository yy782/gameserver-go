package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gameserver/internal/rpc"
)

// newTestServer 构造一个不依赖外部服务的管理面服务器（下游依赖均置空，
// 仅用于测试登录鉴权与路由规则）。
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	svc := NewApiService("api", "127.0.0.1", 9700, nil, nil, nil, rpc.ServiceAddr{})
	return NewServer(svc, Config{
		AdminAccount:  "admin",
		AdminPassword: "admin123",
		JWTSecret:     "test-secret",
		JWTExpireSec:  3600,
	}).Handler()
}

func doReq(t *testing.T, h http.Handler, method, path, body, token string) (int, map[string]interface{}) {
	t.Helper()
	var rdr *bytes.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var out map[string]interface{}
	if len(w.Body.Bytes()) > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是合法 JSON: %v, body=%s", err, w.Body.String())
		}
	}
	return w.Code, out
}

// TestRequireAuthReject 未带/伪造 token 访问受保护接口应 401
func TestRequireAuthReject(t *testing.T) {
	h := newTestServer(t)

	if code, _ := doReq(t, h, http.MethodGet, "/api/v1/me", "", ""); code != http.StatusUnauthorized {
		t.Errorf("无 token 应 401，got %d", code)
	}
	if code, _ := doReq(t, h, http.MethodGet, "/api/v1/me", "", "junk-token"); code != http.StatusUnauthorized {
		t.Errorf("非法 token 应 401，got %d", code)
	}
	if code, _ := doReq(t, h, http.MethodGet, "/api/v1/servers", "", ""); code != http.StatusUnauthorized {
		t.Errorf("servers 无 token 应 401，got %d", code)
	}
	if code, _ := doReq(t, h, http.MethodGet, "/api/v1/rank/top", "", ""); code != http.StatusUnauthorized {
		t.Errorf("rank 无 token 应 401，got %d", code)
	}
}

// TestAdminLoginWrongPassword 错误密码登录失败
func TestAdminLoginWrongPassword(t *testing.T) {
	h := newTestServer(t)
	code, out := doReq(t, h, http.MethodPost, "/api/v1/admin/login",
		`{"account":"admin","password":"wrong"}`, "")
	if code != http.StatusUnauthorized {
		t.Errorf("错误密码应 401，got %d", code)
	}
	if out["code"].(float64) != http.StatusUnauthorized {
		t.Errorf("业务码应为 401，got %v", out["code"])
	}
}

// TestAdminLoginBadBody 非法请求体 400
func TestAdminLoginBadBody(t *testing.T) {
	h := newTestServer(t)
	if code, _ := doReq(t, h, http.MethodPost, "/api/v1/admin/login",
		`{"account":123}`, ""); code != http.StatusBadRequest {
		t.Errorf("非法请求体应 400，got %d", code)
	}
	if code, _ := doReq(t, h, http.MethodPost, "/api/v1/admin/login", "not-json", ""); code != http.StatusBadRequest {
		t.Errorf("非 JSON 应 400，got %d", code)
	}
}

// TestAdminLoginAndAccess 登录成功后携带 token 可访问受保护接口
func TestAdminLoginAndAccess(t *testing.T) {
	h := newTestServer(t)
	code, out := doReq(t, h, http.MethodPost, "/api/v1/admin/login",
		`{"account":"admin","password":"admin123"}`, "")
	if code != http.StatusOK {
		t.Fatalf("正确密码应登录成功，got %d", code)
	}
	data := out["data"].(map[string]interface{})
	token, ok := data["token"].(string)
	if !ok || token == "" {
		t.Fatalf("登录响应缺少 token: %v", data)
	}

	code, out = doReq(t, h, http.MethodGet, "/api/v1/me", "", token)
	if code != http.StatusOK {
		t.Fatalf("携带有效 token 应 200，got %d", code)
	}
	me := out["data"].(map[string]interface{})
	if me["account"] != "admin" {
		t.Errorf("me 应返回运营账号 admin，got %v", me)
	}
}

// TestHealthz 健康检查无需鉴权（下游依赖缺失时返回 503 但路由可达）
func TestHealthz(t *testing.T) {
	h := newTestServer(t)
	code, out := doReq(t, h, http.MethodGet, "/healthz", "", "")
	if code != http.StatusOK && code != http.StatusServiceUnavailable {
		t.Errorf("healthz 应可匿名访问，got %d", code)
	}
	if _, ok := out["data"]; !ok {
		t.Errorf("healthz 应带依赖状态 data，got %v", out)
	}
}
