package common

import (
	"context"
	"time"
)

// NewContextWithTimeout 创建带超时的上下文
// 调用方必须 defer cancel() 释放资源
func NewContextWithTimeout(duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), duration)
}
