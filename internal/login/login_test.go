package login

// 登录服无外部依赖的纯逻辑测试：DB 行 -> protobuf 玩家映射、会话 token 生成。
// （Authenticate / Register 依赖真实 MySQL + Redis 客户端，不在单测范围。）
import (
	"testing"

	"gameserver/internal/rpc"
)

func TestToPlayerBaseMapping(t *testing.T) {
	p := &rpc.Player{
		ID: 123, Name: "hero", Level: 5, Exp: 999, Gold: 42,
		Score: 7, ServerID: 1,
	}
	got := toPlayerBase(p)
	if got.PlayerId != 123 || got.Name != "hero" || got.Level != 5 ||
		got.Exp != 999 || got.Gold != 42 || got.Score != 7 || got.ServerId != 1 {
		t.Fatalf("toPlayerBase 映射异常: %+v", got)
	}
}

func TestGenerateToken(t *testing.T) {
	a := generateToken()
	b := generateToken()
	if len(a) != 32 { // 16 字节随机 hex
		t.Fatalf("token 长度=%d, want 32", len(a))
	}
	if a == b {
		t.Fatal("两次生成的 token 不应相同")
	}
}
