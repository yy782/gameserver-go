package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Resp 统一响应体：code=0 表示成功；code 非 0 时与 HTTP 状态码语义对齐（如 401/404/502）
type Resp struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

// writeOK 成功响应
func writeOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Resp{Code: 0, Msg: "ok", Data: data})
}

// writeErr 失败响应（中止后续 handler）
func writeErr(c *gin.Context, status, code int, msg string) {
	c.AbortWithStatusJSON(status, Resp{Code: code, Msg: msg})
}
