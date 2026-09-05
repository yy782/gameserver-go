package common

// 哈希与随机工具单元测试（internal/common/hash.go）。
import (
	"strings"
	"testing"
)

func TestHashHexProperties(t *testing.T) {
	// 输出固定为 64 位小写 hex（4 组 16 位）
	h := HashHex("hello")
	if len(h) != 64 {
		t.Fatalf("哈希长度=%d, want 64", len(h))
	}
	for _, c := range h {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("输出含非法字符 %q", c)
		}
	}
	// 确定性
	if HashHex("hello") != h {
		t.Fatal("相同输入应得到相同哈希")
	}
	// 不同输入应不同（常规情形）
	if HashHex("hello") == HashHex("hellp") {
		t.Fatal("不同输入不应撞哈希")
	}
	// 空串可计算且非空
	if HashHex("") == "" {
		t.Fatal("空串也应有输出")
	}
}

func TestHashWithSaltAndVerify(t *testing.T) {
	salt := GenerateSalt()
	if len(salt) != 6 {
		t.Fatalf("盐值长度=%d, want 6", len(salt))
	}
	h := HashWithSalt("secret", salt)
	// 加盐规则 = HashHex(salt + input)
	if h != HashHex(salt+"secret") {
		t.Fatal("加盐哈希规则应等于 HashHex(salt+input)")
	}
	if VerifyHash(salt+"secret", h) != true {
		t.Fatal("VerifyHash 应校验通过")
	}
	if VerifyHash("wrong", h) != false {
		t.Fatal("错误输入不应通过校验")
	}
}

func TestGenerateSaltRange(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := GenerateSalt()
		if len(s) != 6 {
			t.Fatalf("盐值长度=%d", len(s))
		}
		if s < "100000" || s > "999999" {
			t.Fatalf("盐值越界: %s", s)
		}
	}
}

func TestRandomInt(t *testing.T) {
	// 边界：min == max
	if RandomInt(7, 7) != 7 {
		t.Fatal("min==max 应返回该值")
	}
	// 非法区间：max < min 直接返回 min
	if RandomInt(9, 3) != 9 {
		t.Fatal("max<min 应返回 min")
	}
	// 区间内抽样
	for i := 0; i < 500; i++ {
		v := RandomInt(5, 10)
		if v < 5 || v > 10 {
			t.Fatalf("随机值越界: %d", v)
		}
	}
}
