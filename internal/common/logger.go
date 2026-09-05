package common

import (
	"fmt"
	"log"
	"os"
)

var logger = log.New(os.Stdout, "", log.LstdFlags)

// Info 输出信息日志
func Info(msg string, args ...interface{}) {
	logger.Printf("[INFO] "+msg, args...)
}

// Warn 输出警告日志
func Warn(msg string, args ...interface{}) {
	logger.Printf("[WARN] "+msg, args...)
}

// Error 输出错误日志
func Error(msg string, args ...interface{}) {
	logger.Printf("[ERROR] "+msg, args...)
}

// Fatal 输出致命错误日志并退出
func Fatal(msg string, args ...interface{}) {
	logger.Fatalf("[FATAL] "+msg, args...)
}

// Debugf 输出调试日志
func Debugf(msg string, args ...interface{}) {
	logger.Printf("[DEBUG] "+msg, args...)
}