package game

// 匹配池成员编解码：member = "player_id|player_name|gateway_addr"。
import "testing"

func TestMatchPoolKey(t *testing.T) {
	if MatchPoolKey(0) != kMatchPoolFsKey {
		t.Fatalf("mode=0 应对应帧同步池, got %q", MatchPoolKey(0))
	}
	if MatchPoolKey(1) != kMatchPoolSsKey || MatchPoolKey(99) != kMatchPoolSsKey {
		t.Fatalf("mode 非 0 应对应状态同步池")
	}
}

func TestEncodeDecodeMemberRoundTrip(t *testing.T) {
	w := MatchWaiter{PlayerID: 12345, PlayerName: "alice", GatewayAddr: "10.0.0.7:8001"}
	got := ParseMember(encodeMember(w))
	if got != w {
		t.Fatalf("往返不一致: got %+v, want %+v", got, w)
	}
}

func TestEncodeDecodeMemberEmptyName(t *testing.T) {
	// 昵称为空时字段位置仍应正确解析
	got := ParseMember(encodeMember(MatchWaiter{PlayerID: 7, PlayerName: "", GatewayAddr: "gw:9000"}))
	if got.PlayerID != 7 || got.PlayerName != "" || got.GatewayAddr != "gw:9000" {
		t.Fatalf("空昵称解析异常: %+v", got)
	}
}

func TestParseMemberMalformed(t *testing.T) {
	// 非数字 player_id：不 panic，字段为 0
	got := ParseMember("abc|alice|gw:1")
	if got.PlayerID != 0 {
		t.Fatalf("非数字 id 应解析为 0, got %d", got.PlayerID)
	}
	// 缺分隔符：不 panic，字段为空
	got = ParseMember("42")
	if got.PlayerID != 0 {
		t.Fatalf("缺分隔符应解析为 0, got %d", got.PlayerID)
	}
	if got := ParseMember(""); got.PlayerID != 0 {
		t.Fatalf("空串应解析为 0")
	}
}
