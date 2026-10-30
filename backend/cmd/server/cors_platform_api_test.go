package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCORSTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_CORS_ORIGINS", "https://canvas.example.com")
	middleware, err := cors()
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(middleware)
	router.Any("/api/open/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.Any("/api/wallet", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

func TestPlatformAPICORSAllowsAnyOriginWithoutCredentials(t *testing.T) {
	router := newCORSTestRouter(t)
	preflight := httptest.NewRequest(http.MethodOptions, "/api/open/v1/messages", nil)
	preflight.Header.Set("Origin", "app://desktop-client")
	preflight.Header.Set("Access-Control-Request-Headers", "x-api-key, content-type, anthropic-version")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight = %d %v", response.Code, response.Header())
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("open API must never allow credentialed cross-origin requests")
	}
	if response.Header().Get("Access-Control-Allow-Headers") != "x-api-key, content-type, anthropic-version" {
		t.Fatalf("allowed headers = %q", response.Header().Get("Access-Control-Allow-Headers"))
	}

	request := httptest.NewRequest(http.MethodPost, "/api/open/v1/messages", nil)
	request.Header.Set("Origin", "app://desktop-client")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("open API request from desktop origin = %d", response.Code)
	}
}

func TestPlatformAPICORSKeepsSiteOriginCheck(t *testing.T) {
	router := newCORSTestRouter(t)
	request := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	request.Header.Set("Origin", "https://evil.example.com")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cookie API must keep rejecting foreign origins, got %d", response.Code)
	}
}
