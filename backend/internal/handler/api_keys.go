package handler

import (
	"errors"
	"net/http"
	"time"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterUserAPIKeyRoutes 是设置页管理平台 API Key 的接口，只认登录 Cookie；
// 不能用 API Key 本身来创建或删除 Key。
func RegisterUserAPIKeyRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/api-keys", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		keys, err := svc.UserAPIKeys(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"keys": keys, "limit": service.MaxUserAPIKeys})
	})
	r.POST("/api-keys", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceRateLimit(c, "api-key-create:"+user.ID, 10, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		var req service.CreateUserAPIKeyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, errors.New("请求格式错误"))
			return
		}
		created, err := svc.CreateUserAPIKey(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		// 明文 Key 只出现在这一次响应里，不允许任何缓存保存。
		c.Header("Cache-Control", "no-store")
		ok(c, created)
	})
	r.DELETE("/api-keys/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteUserAPIKey(user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": true})
	})
}
