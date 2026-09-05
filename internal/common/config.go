package common

import (
	"encoding/json"
	"os"
)

// Config JSON 配置管理
type Config struct {
	m map[string]interface{}
}

// NewConfig 创建配置实例
func NewConfig() *Config {
	return &Config{
		m: make(map[string]interface{}),
	}
}

// LoadFile 从 JSON 文件加载配置
func (c *Config) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &c.m)
}

// GetString 获取字符串配置
func (c *Config) GetString(key string, def string) string {
	if v, ok := c.m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// GetInt 获取整数配置
func (c *Config) GetInt(key string, def int64) int64 {
	if v, ok := c.m[key]; ok {
		switch val := v.(type) {
		case float64:
			return int64(val)
		case int:
			return int64(val)
		}
	}
	return def
}

// GetBool 获取布尔配置
func (c *Config) GetBool(key string, def bool) bool {
	if v, ok := c.m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// GetFloat 获取浮点配置
func (c *Config) GetFloat(key string, def float64) float64 {
	if v, ok := c.m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return def
}

// Has 检查配置是否存在
func (c *Config) Has(key string) bool {
	_, ok := c.m[key]
	return ok
}