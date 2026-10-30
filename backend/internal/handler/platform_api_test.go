package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const platformTestCookie = "platform-session.platform-token"

func newPlatformAPITestRouter(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "platform-api.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database.Models()...); err != nil {
		t.Fatal(err)
	}
	user := &model.User{ID: "platform-user", Username: "platform", Role: model.UserRoleUser, Status: model.UserStatusActive}
	for _, item := range []any{
		user,
		&model.AuthSession{ID: "platform-session", UserID: user.ID, TokenHash: auth.HashToken("platform-token"), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: user.ID, AvailableMicrocredits: 5_000_000},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() {
		ConfigureRuntime(previous)
		svc.Close()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	created, err := svc.CreateUserAPIKey(user, service.CreateUserAPIKeyRequest{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	api := router.Group("/api")
	RegisterUserAPIKeyRoutes(api, svc)
	RegisterPlatformAPIRoutes(api, svc)
	return router, svc, created.Secret
}

func servePlatformRequest(router *gin.Engine, method, path string, body any, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if key == "Cookie" {
			request.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: value})
			continue
		}
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var payload map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	return response, payload
}

func nestedString(payload map[string]any, keys ...string) string {
	var current any = payload
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	value, _ := current.(string)
	return value
}

func TestPlatformAPIRejectsMissingWrongAndCookieOnlyCredentials(t *testing.T) {
	router, _, _ := newPlatformAPITestRouter(t)
	for name, headers := range map[string]map[string]string{
		"missing key": nil,
		"wrong key":   {"Authorization": "Bearer " + service.UserAPIKeyPrefix + strings.Repeat("0", 64)},
		"cookie only": {"Cookie": platformTestCookie},
	} {
		response, payload := servePlatformRequest(router, http.MethodGet, "/api/open/v1/balance", nil, headers)
		if response.Code != http.StatusUnauthorized || nestedString(payload, "error", "type") != "authentication_error" {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body.String())
		}
	}
	response, payload := servePlatformRequest(router, http.MethodPost, "/api/open/v1/messages", map[string]any{"model": "claude-test"}, nil)
	if response.Code != http.StatusUnauthorized || payload["type"] != "error" || nestedString(payload, "error", "type") != "authentication_error" {
		t.Fatalf("Claude endpoint must answer in Claude error format: %d %s", response.Code, response.Body.String())
	}
}

func TestPlatformAPIAcceptsBearerAndXAPIKey(t *testing.T) {
	router, _, secret := newPlatformAPITestRouter(t)
	response, payload := servePlatformRequest(router, http.MethodGet, "/api/open/v1/balance", nil, map[string]string{"Authorization": "Bearer " + secret})
	if response.Code != http.StatusOK || payload["object"] != "balance" || payload["available_microcredits"] != float64(5_000_000) || payload["available_credits"] != float64(5) {
		t.Fatalf("balance = %d %s", response.Code, response.Body.String())
	}
	response, payload = servePlatformRequest(router, http.MethodGet, "/api/open/v1/models", nil, map[string]string{"x-api-key": secret})
	if response.Code != http.StatusOK || payload["object"] != "list" {
		t.Fatalf("models = %d %s", response.Code, response.Body.String())
	}
	if _, ok := payload["data"].([]any); !ok {
		t.Fatalf("models data must be an array: %s", response.Body.String())
	}
	response, payload = servePlatformRequest(router, http.MethodPost, "/api/open/v1/messages", map[string]any{"model": "missing-model", "max_tokens": 10}, map[string]string{"x-api-key": secret})
	if response.Code != http.StatusNotFound || nestedString(payload, "error", "type") != "not_found_error" {
		t.Fatalf("unknown model = %d %s", response.Code, response.Body.String())
	}
	response, payload = servePlatformRequest(router, http.MethodPost, "/api/open/v1/tasks", map[string]any{"model": "missing-model", "prompt": "x"}, map[string]string{"Authorization": "Bearer " + secret})
	if response.Code != http.StatusNotFound || nestedString(payload, "error", "type") != "invalid_request_error" {
		t.Fatalf("unknown task model = %d %s", response.Code, response.Body.String())
	}
}

func TestUserAPIKeyRoutesRequireSessionCookie(t *testing.T) {
	router, _, secret := newPlatformAPITestRouter(t)
	response, _ := servePlatformRequest(router, http.MethodPost, "/api/api-keys", map[string]any{"name": "via key"}, map[string]string{"Authorization": "Bearer " + secret})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("an API key must not create keys: %d %s", response.Code, response.Body.String())
	}
	response, payload := servePlatformRequest(router, http.MethodPost, "/api/api-keys", map[string]any{"name": "笔记本"}, map[string]string{"Cookie": platformTestCookie})
	if response.Code != http.StatusOK || !strings.HasPrefix(nestedString(payload, "data", "secret"), service.UserAPIKeyPrefix) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create = %d %s %v", response.Code, response.Body.String(), response.Header())
	}
	createdID := nestedString(payload, "data", "key", "id")
	response, _ = servePlatformRequest(router, http.MethodGet, "/api/api-keys", nil, map[string]string{"Cookie": platformTestCookie})
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "keyHash") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("list must not expose secrets or hashes: %s", response.Body.String())
	}
	response, _ = servePlatformRequest(router, http.MethodDelete, "/api/api-keys/"+createdID, nil, map[string]string{"Cookie": platformTestCookie})
	if response.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}
	response, _ = servePlatformRequest(router, http.MethodDelete, "/api/api-keys/"+createdID, nil, map[string]string{"Cookie": platformTestCookie})
	if response.Code != http.StatusNotFound {
		t.Fatalf("delete twice = %d %s", response.Code, response.Body.String())
	}
}

func TestPlatformAPILimitsRequestsPerIPBeforeCheckingKey(t *testing.T) {
	router, _, _ := newPlatformAPITestRouter(t)
	wrongKey := map[string]string{"Authorization": "Bearer " + service.UserAPIKeyPrefix + strings.Repeat("0", 64)}
	for index := 0; index < platformAPIRequestsPerIPPerMinute; index++ {
		if response, _ := servePlatformRequest(router, http.MethodGet, "/api/open/v1/balance", nil, wrongKey); response.Code != http.StatusUnauthorized {
			t.Fatalf("request %d = %d %s", index, response.Code, response.Body.String())
		}
	}
	response, payload := servePlatformRequest(router, http.MethodGet, "/api/open/v1/balance", nil, wrongKey)
	if response.Code != http.StatusTooManyRequests || nestedString(payload, "error", "type") != "rate_limit_error" || response.Header().Get("Retry-After") == "" {
		t.Fatalf("over the IP limit = %d %s %v", response.Code, response.Body.String(), response.Header())
	}
	response, payload = servePlatformRequest(router, http.MethodPost, "/api/open/v1/messages", map[string]any{"model": "claude-test"}, wrongKey)
	if response.Code != http.StatusTooManyRequests || payload["type"] != "error" || nestedString(payload, "error", "type") != "rate_limit_error" {
		t.Fatalf("Claude endpoint over the IP limit = %d %s", response.Code, response.Body.String())
	}
}
