package common

// 通用工具函数单元测试（internal/common/util.go）。
import (
	"testing"
)

func TestNowMs(t *testing.T) {
	a := NowMs()
	b := NowMs()
	if b < a {
		t.Fatalf("时间戳倒退: %d > %d", a, b)
	}
	if a <= 0 {
		t.Fatal("时间戳应为正数")
	}
}

func TestDistAndDistSq(t *testing.T) {
	// 3-4-5 直角三角形
	if d := DistSq(0, 0, 3, 4); d != 25 {
		t.Fatalf("DistSq=%v, want 25", d)
	}
	if d := Dist(0, 0, 3, 4); d != 5 {
		t.Fatalf("Dist=%v, want 5", d)
	}
	if DistSq(1, 1, 1, 1) != 0 {
		t.Fatal("同点距离平方应为 0")
	}
}

func TestGenerateIDUnique(t *testing.T) {
	seen := map[int64]bool{}
	for i := 0; i < 1000; i++ {
		id := GenerateID()
		if id <= 0 {
			t.Fatalf("id 应为正数: %d", id)
		}
		if seen[id] {
			t.Fatalf("id 重复: %d", id)
		}
		seen[id] = true
	}
}

func TestGenerateRoomID(t *testing.T) {
	a := GenerateRoomID()
	b := GenerateRoomID()
	if a == b {
		t.Fatal("房间号不应重复")
	}
}

func TestFormatAddr(t *testing.T) {
	if FormatAddr("127.0.0.1", 8000) != "127.0.0.1:8000" {
		t.Fatalf("FormatAddr 结果异常: %s", FormatAddr("127.0.0.1", 8000))
	}
	if FormatAddr("::1", 80) != "::1:80" {
		t.Fatalf("IPv6 地址格式化异常: %s", FormatAddr("::1", 80))
	}
}
