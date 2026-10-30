package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const platformAPIPathPrefix = "/api/open/"

// platformAPICORS 处理平台开放接口的跨域。开放接口只认 API Key、从不读取登录 Cookie，
// 来源校验防不住任何东西；桌面客户端（内嵌浏览器）可能带 Origin，因此允许任意来源，但不允许携带凭据。
// 返回 true 表示已处理，调用方不再执行站点自身的来源校验。
func platformAPICORS(c *gin.Context) bool {
	if !strings.HasPrefix(c.Request.URL.Path, platformAPIPathPrefix) {
		return false
	}
	if c.GetHeader("Origin") != "" {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Vary", "Origin, Access-Control-Request-Headers")
		allowedHeaders := "Authorization, Content-Type, x-api-key, anthropic-version, anthropic-beta"
		if requested := strings.TrimSpace(c.GetHeader("Access-Control-Request-Headers")); requested != "" {
			allowedHeaders = requested
		}
		c.Header("Access-Control-Allow-Headers", allowedHeaders)
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Expose-Headers", "Retry-After, X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")
	}
	if c.Request.Method == http.MethodOptions {
		c.AbortWithStatus(http.StatusNoContent)
		return true
	}
	c.Next()
	return true
}
