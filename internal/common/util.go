package common

import (
	"fmt"
	"math"
	"time"
)

// NowMs 获取当前毫秒时间戳
func NowMs() int64 {
	return time.Now().UnixMilli()
}

// DistSq 计算两点间距离平方
func DistSq(x1, y1, x2, y2 float32) float32 {
	dx := x1 - x2
	dy := y1 - y2
	return dx*dx + dy*dy
}

// Dist 计算两点间距离
func Dist(x1, y1, x2, y2 float32) float32 {
	return float32(math.Sqrt(float64(DistSq(x1, y1, x2, y2))))
}

// GenerateID 生成唯一ID
func GenerateID() int64 {
	return NowMs()<<16 | (int64(time.Now().Nanosecond()) & 0xFFFF)
}

// GenerateRoomID 生成房间ID
func GenerateRoomID() int64 {
	return NowMs()<<16 | (int64(time.Now().Nanosecond()) & 0xFFFF)
}

// FormatAddr 格式化地址
func FormatAddr(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}