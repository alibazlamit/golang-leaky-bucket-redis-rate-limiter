package leaky_bucket_redis

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestEchoMiddleware(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	rate := 10.0
	lb := New(client, rate)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	extractor := func(r *http.Request) string {
		return "echo_test_user"
	}

	var onLimitCalled bool
	mw := EchoMiddleware(lb, extractor, WithOnLimit(func(r *http.Request, res *Result) {
		onLimitCalled = true
	}))

	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	// First request - OK
	err := handler(c)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	err = handler(c)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// We only get 1 token initially since Burst is not set, so 2nd request should be rate limited immediately
    // Wait, by default burst is 1 for New. The first request passes. The second request right after will fail.
	
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)

	err2 := handler(c2)
    // Echo returns error by returning c.JSON inside middleware or by simply completing without next()
    // It actually returns `c.JSON(http.StatusTooManyRequests, ...)` which echoes it directly to the response writer, but returns nil from the handler itself since error is handled.
	if err2 != nil {
		t.Fatalf("Unexpected error: %v", err2)
	}
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

func TestEchoMiddleware_FailOpen(t *testing.T) {
	// Create an invalid client to force a connection issue.
	// We'll just close it immediately.
	client := createTestClient(t)
	client.Close()

	lb := New(client, 10.0)
	
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	extractor := func(r *http.Request) string {
		return "fail_open_echo"
	}

	mw := EchoMiddleware(lb, extractor)
	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	err := handler(c)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("Expected fail open with status 200, got %d", rec.Code)
	}
}
