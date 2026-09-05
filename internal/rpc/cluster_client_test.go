package rpc

// ClusterClient 纯内存部分单元测试：服务列表刷新与 round-robin 选服。
import (
	"testing"
)

func TestPickGameRoundRobin(t *testing.T) {
	cc := NewClusterClient()
	addrs := []ServiceAddr{
		{Host: "10.0.0.1", Port: 9001},
		{Host: "10.0.0.2", Port: 9002},
		{Host: "10.0.0.3", Port: 9003},
	}
	if cc.GameCount() != 0 {
		t.Fatal("初始 game 数应为 0")
	}
	cc.SetGameList(addrs)
	if cc.GameCount() != 3 {
		t.Fatalf("GameCount=%d, want 3", cc.GameCount())
	}

	// 连续轮转应依次回到起点
	want := make([]ServiceAddr, 0, 9)
	for i := 0; i < 9; i++ {
		want = append(want, addrs[i%len(addrs)])
	}
	for i, w := range want {
		got, ok := cc.PickGame()
		if !ok || got != w {
			t.Fatalf("第 %d 次 PickGame=%v ok=%v, want %v", i, got, ok, w)
		}
	}
}

func TestPickGameEmptyList(t *testing.T) {
	cc := NewClusterClient()
	if got, ok := cc.PickGame(); ok || got != (ServiceAddr{}) {
		t.Fatalf("空列表应返回 false, got %v ok=%v", got, ok)
	}
}

func TestPickGameListRefresh(t *testing.T) {
	cc := NewClusterClient()
	cc.SetGameList([]ServiceAddr{{Host: "old", Port: 1}})
	cc.SetGameList([]ServiceAddr{{Host: "new", Port: 2}, {Host: "new2", Port: 3}})
	if cc.GameCount() != 2 {
		t.Fatalf("刷新后 GameCount=%d, want 2", cc.GameCount())
	}
	got, ok := cc.PickGame()
	if !ok || got.Host != "new" {
		t.Fatalf("刷新后首个 PickGame=%v, want new", got)
	}
}
