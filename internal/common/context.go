package common

import (
	"context"
	"time"
)

// NewContextWithTimeout 创建带超时的上下文
func NewContextWithTimeout(duration time.Duration) context.Context {
	ctx, _ := context.WithTimeout(context.Background(), duration)
	return ctx
}