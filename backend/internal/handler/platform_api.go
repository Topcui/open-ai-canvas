package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// 平台开放接口只认 API Key，从不读取登录 Cookie；认出用户后文本交给系统渠道转发，
// 生图、生视频交给任务链路，权限、计费和流式输出都沿用原有实现。

const (
	// platformAPIRequestsPerIPPerMinute 在认 Key 之前按来源 IP 限流，挡住拿接口批量试 Key 造成的查库压力；
	// 认出用户后各接口仍按用户再限流（系统渠道转发、任务创建沿用原有频控）。
	platformAPIRequestsPerIPPerMinute = 600
	platformCountTokensPerMinute      = 120
	platformAPIScene                  = "platform_api"
	platformTaskRequestMaxBytes       = 1 << 20
)

type platformErrorFormat int

const (
	platformErrorOpenAI platformErrorFormat = iota
	platformErrorClaude
)

func RegisterPlatformAPIRoutes(r *gin.RouterGroup, svc *service.Service) {
	open := r.Group("/open/v1")
	open.GET("/models", func(c *gin.Context) {
		user, authorized := platformAPIUser(c, svc, platformErrorOpenAI)
		if !authorized {
			return
		}
		models, err := svc.PlatformModels(user)
		if err != nil {
			writePlatformError(c, platformErrorOpenAI, err)
			return
		}
		response := gin.H{"object": "list", "data": models, "has_more": false}
		if len(models) > 0 {
			response["first_id"], response["last_id"] = models[0].ID, models[len(models)-1].ID
		}
		c.JSON(http.StatusOK, response)
	})
	open.POST("/messages", func(c *gin.Context) {
		proxyPlatformChat(c, svc, service.PlatformEndpointMessages, "/messages", platformErrorClaude)
	})
	open.POST("/messages/count_tokens", func(c *gin.Context) {
		user, authorized := platformAPIUser(c, svc, platformErrorClaude)
		if !authorized || !allowPlatformRequest(c, "platform-count-tokens:"+user.ID, platformCountTokensPerMinute, platformErrorClaude) {
			return
		}
		body, readable := readPlatformChatBody(c, svc, platformErrorClaude)
		if !readable {
			return
		}
		if _, err := svc.ResolvePlatformModel(user, proxyRequestModel(c.GetHeader("Content-Type"), body), service.PlatformEndpointMessages); err != nil {
			writePlatformError(c, platformErrorClaude, err)
			return
		}
		// 本地估算，不调用上游也不计费；口径与预授权冻结积分一致。
		c.JSON(http.StatusOK, gin.H{"input_tokens": service.EstimatePlatformInputTokens(body)})
	})
	open.POST("/chat/completions", func(c *gin.Context) {
		proxyPlatformChat(c, svc, service.PlatformEndpointChatCompletions, "/chat/completions", platformErrorOpenAI)
	})
	open.POST("/tasks", func(c *gin.Context) {
		user, authorized := platformAPIUser(c, svc, platformErrorOpenAI)
		if !authorized {
			return
		}
		policy, err := svc.RuntimePolicy()
		if err != nil {
			writePlatformError(c, platformErrorOpenAI, service.WrapAppError(http.StatusServiceUnavailable, "运行策略暂时不可用", err))
			return
		}
		// 与网页共用同一个任务创建频控，站外调用不能绕过每分钟上限。
		if !allowPlatformRequest(c, "tasks:"+user.ID, policy.Request.TaskCreatePerMinute, platformErrorOpenAI) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, platformTaskRequestMaxBytes)
		var req service.PlatformTaskRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writePlatformError(c, platformErrorOpenAI, service.BadAuthRequest("请求格式错误"))
			return
		}
		task, err := svc.CreatePlatformTask(user, req, TraceID(c), RequestID(c))
		if err != nil {
			writePlatformError(c, platformErrorOpenAI, err)
			return
		}
		c.JSON(http.StatusOK, platformTaskWithAbsoluteURLs(c, task))
	})
	open.GET("/tasks/:id", func(c *gin.Context) {
		user, authorized := platformAPIUser(c, svc, platformErrorOpenAI)
		if !authorized {
			return
		}
		task, err := svc.PlatformTask(user, c.Param("id"))
		if err != nil {
			writePlatformError(c, platformErrorOpenAI, err)
			return
		}
		c.JSON(http.StatusOK, platformTaskWithAbsoluteURLs(c, task))
	})
	open.GET("/balance", func(c *gin.Context) {
		user, authorized := platformAPIUser(c, svc, platformErrorOpenAI)
		if !authorized {
			return
		}
		balance, err := svc.PlatformBalance(user)
		if err != nil {
			writePlatformError(c, platformErrorOpenAI, err)
			return
		}
		c.JSON(http.StatusOK, balance)
	})
}

// proxyPlatformChat 按模型名找到渠道后，把原始请求体交给系统渠道转发；转发内的权限、限流、
// 预授权、流式透传和按用量结算保持不变。
func proxyPlatformChat(c *gin.Context, svc *service.Service, endpoint string, providerPath string, format platformErrorFormat) {
	user, authorized := platformAPIUser(c, svc, format)
	if !authorized {
		return
	}
	body, readable := readPlatformChatBody(c, svc, format)
	if !readable {
		return
	}
	target, err := svc.ResolvePlatformModel(user, proxyRequestModel(c.GetHeader("Content-Type"), body), endpoint)
	if err != nil {
		writePlatformError(c, format, err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	// 账单场景标成开放接口，后台能区分站外调用；客户端自带的幂等键不参与系统渠道计费。
	c.Request.Header.Set("X-Canvas-Scene", platformAPIScene)
	c.Request.Header.Del("X-Idempotency-Key")
	proxySystemRequestPath(c, svc, user, target.Channel, providerPath)
}

func readPlatformChatBody(c *gin.Context, svc *service.Service, format platformErrorFormat) ([]byte, bool) {
	policy, err := svc.RuntimePolicy()
	if err != nil {
		writePlatformError(c, format, service.WrapAppError(http.StatusServiceUnavailable, "运行策略暂时不可用", err))
		return nil, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, policy.Request.SystemRelayRequestMB<<20)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writePlatformError(c, format, service.BadAuthRequest("请求体过大或读取失败"))
		return nil, false
	}
	return body, true
}

// platformAPIKey 读取 Claude（x-api-key）和 OpenAI（Authorization: Bearer）两种写法；不接受地址参数，避免 Key 进访问日志。
func platformAPIKey(c *gin.Context) string {
	if value := strings.TrimSpace(c.GetHeader("x-api-key")); value != "" {
		return value
	}
	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(authorization) > len("Bearer ") && strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authorization[len("Bearer "):])
	}
	return ""
}

func platformAPIUser(c *gin.Context, svc *service.Service, format platformErrorFormat) (*model.User, bool) {
	if !allowPlatformRequest(c, "platform-api-ip:"+c.ClientIP(), platformAPIRequestsPerIPPerMinute, format) {
		return nil, false
	}
	secret := platformAPIKey(c)
	if secret == "" {
		writePlatformError(c, format, service.Unauthorized("缺少 API Key，请在请求头 Authorization: Bearer 或 x-api-key 中提供"))
		return nil, false
	}
	user, err := svc.AuthenticateUserAPIKey(secret)
	if err != nil {
		writePlatformError(c, format, err)
		return nil, false
	}
	return user, true
}

// allowPlatformRequest 与 enforceRateLimit 使用同一个协调器计数，但按开放接口的协议格式返回 429。
func allowPlatformRequest(c *gin.Context, key string, limit int, format platformErrorFormat) bool {
	if runtimeService == nil {
		writePlatformError(c, format, service.NewAppError(http.StatusServiceUnavailable, "请求协调器尚未初始化"))
		return false
	}
	allowed, err := runtimeService.AllowRequest(c.Request.Context(), key, limit, time.Minute)
	if err != nil {
		writePlatformError(c, format, service.NewAppError(http.StatusServiceUnavailable, "请求协调服务暂时不可用"))
		return false
	}
	if allowed {
		return true
	}
	wait := runtimeService.RequestRetryAfter(c.Request.Context(), key, time.Minute)
	seconds := max(1, int((wait+time.Second-1)/time.Second))
	c.Header("Retry-After", strconv.Itoa(seconds))
	writePlatformError(c, format, service.RateLimited(fmt.Sprintf("请求次数已达上限，请在 %d 秒后重试", seconds)))
	return false
}

// writePlatformError 按调用方使用的协议返回标准错误格式，Claude Code、OpenAI SDK 等客户端能直接显示 message。
func writePlatformError(c *gin.Context, format platformErrorFormat, err error) {
	status, message, reason := http.StatusInternalServerError, safeInternalErrorMessage(http.StatusInternalServerError), string(service.ReasonForStatus(http.StatusInternalServerError))
	var modelErr *service.ModelError
	var appErr *service.AppError
	if errors.As(err, &modelErr) && modelErr.AppError != nil {
		appErr = modelErr.AppError
	}
	if appErr != nil || errors.As(err, &appErr) {
		if validErrorStatus(appErr.Status) {
			status = appErr.Status
			if text := strings.TrimSpace(appErr.Message); text != "" {
				message = text
			}
			reason = string(appErr.Reason)
			if reason == "" {
				reason = string(service.ReasonForStatus(status))
			}
		}
	}
	if status >= http.StatusInternalServerError {
		logHandlerError(c, status, err)
	}
	if format == platformErrorClaude {
		c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": claudeErrorType(status), "message": message}})
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": openAIErrorType(status), "code": reason}})
}

func claudeErrorType(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	}
	return "api_error"
}

func openAIErrorType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	}
	if status >= http.StatusInternalServerError {
		return "server_error"
	}
	return "invalid_request_error"
}

// platformTaskWithAbsoluteURLs 是兜底：服务层已优先用「服务器访问地址」补全本地存储的相对地址，
// 只有站点没配置该地址时才按请求域名补全（反向代理未转发端口时，非 80/443 部署需配置服务器访问地址）。
func platformTaskWithAbsoluteURLs(c *gin.Context, task *service.PlatformTask) *service.PlatformTask {
	if task == nil {
		return nil
	}
	for index := range task.Outputs {
		value := task.Outputs[index].URL
		if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
			continue
		}
		scheme := "http"
		if c.Request.TLS != nil || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https") {
			scheme = "https"
		}
		task.Outputs[index].URL = scheme + "://" + c.Request.Host + value
	}
	return task
}
