package common

// Config JSON 读取单元测试（internal/common/config.go）。
import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写临时配置失败: %v", err)
	}
	return path
}

func TestConfigLoadAndTypes(t *testing.T) {
	path := writeTempJSON(t, `{
	  "host": "127.0.0.1",
	  "port": 8000,
	  "rate": 0.75,
	  "debug": true,
	  "mode": 1
	}`)
	c := NewConfig()
	if err := c.LoadFile(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if !c.Has("host") || c.Has("missing") {
		t.Fatal("Has 行为异常")
	}
	if c.GetString("host", "x") != "127.0.0.1" {
		t.Fatalf("GetString=%q", c.GetString("host", "x"))
	}
	// JSON 数字 unmarshal 成 float64，GetInt 需转换
	if c.GetInt("port", 0) != 8000 || c.GetInt("mode", 0) != 1 {
		t.Fatalf("GetInt 异常: %d %d", c.GetInt("port", 0), c.GetInt("mode", 0))
	}
	if c.GetFloat("rate", 0) != 0.75 {
		t.Fatalf("GetFloat 异常: %v", c.GetFloat("rate", 0))
	}
	if !c.GetBool("debug", false) {
		t.Fatal("GetBool 应为 true")
	}
}

func TestConfigDefaults(t *testing.T) {
	c := NewConfig()
	// 空配置下返回默认值，类型不匹配也回退默认
	if c.GetString("x", "def") != "def" {
		t.Fatal("默认字符串异常")
	}
	if c.GetInt("x", 42) != 42 || c.GetFloat("x", 1.5) != 1.5 {
		t.Fatal("默认数值异常")
	}
	if c.GetBool("x", true) != true {
		t.Fatal("默认布尔异常")
	}
}

func TestConfigTypeMismatchFallsBack(t *testing.T) {
	path := writeTempJSON(t, `{"s": 123, "i": "not-a-number", "b": "yes"}`)
	c := NewConfig()
	if err := c.LoadFile(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if c.GetString("s", "def") != "def" {
		t.Fatal("数字读成字符串应回退默认")
	}
	if c.GetInt("i", 7) != 7 {
		t.Fatal("字符串读成整数应回退默认")
	}
	if c.GetBool("b", false) {
		t.Fatal("字符串读成布尔应回退默认")
	}
}

func TestConfigLoadMissingFile(t *testing.T) {
	c := NewConfig()
	if err := c.LoadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("不存在的文件应报错")
	}
}

func TestConfigLoadInvalidJSON(t *testing.T) {
	path := writeTempJSON(t, `{"broken": `)
	c := NewConfig()
	if err := c.LoadFile(path); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}
