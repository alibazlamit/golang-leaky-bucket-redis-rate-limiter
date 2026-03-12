package leaky_bucket_redis

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGinMiddleware(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	rate := 10.0
	lb := New(client, rate)

	gin.SetMode(gin.TestMode)
	router := gin.New()

	extractor := func(r *http.Request) string {
		return "gin_test_user"
	}

	var onLimitCalled bool
	router.Use(GinMiddleware(lb, extractor, WithOnLimit(func(r *http.Request, res *Result) {
		onLimitCalled = true
	})))

	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	// First request - OK
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec1.Code)
	}

	// Second request - Rate Limited
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", rec2.Code)
	}
	
	if !onLimitCalled {
		t.Error("onLimit callback was not called")
	}

	limitHeader := rec2.Header().Get("X-RateLimit-Limit")
	if limitHeader == "" {
		t.Error("Expected X-RateLimit-Limit header")
	}
}

func TestGinMiddleware_FailOpen(t *testing.T) {
	client := createTestClient(t)
	client.Close()

	lb := New(client, 10.0)

	gin.SetMode(gin.TestMode)
	router := gin.New()

	extractor := func(r *http.Request) string {
		return "fail_open_gin"
	}

	router.Use(GinMiddleware(lb, extractor))

	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Should fail open
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}
