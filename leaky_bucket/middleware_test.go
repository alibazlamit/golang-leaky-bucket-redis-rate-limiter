package leaky_bucket_redis

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	
	// Test X-Forwarded-For
	req.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.1")
	if ip := ExtractIP(req); ip != "192.168.1.1, 10.0.0.1" {
		t.Errorf("Expected 192.168.1.1, 10.0.0.1, got %s", ip)
	}
	
	// Test X-Real-IP
	req.Header.Del("X-Forwarded-For")
	req.Header.Set("X-Real-IP", "10.0.0.2")
	if ip := ExtractIP(req); ip != "10.0.0.2" {
		t.Errorf("Expected 10.0.0.2, got %s", ip)
	}
	
	// Test RemoteAddr
	req.Header.Del("X-Real-IP")
	req.RemoteAddr = "127.0.0.1:12345"
	if ip := ExtractIP(req); ip != "127.0.0.1" {
		t.Errorf("Expected 127.0.0.1, got %s", ip)
	}

	// Test fallback string
	req.RemoteAddr = "invalid-format"
	if ip := ExtractIP(req); ip != "invalid-format" {
		t.Errorf("Expected invalid-format, got %s", ip)
	}
}

func TestExtractHeader(t *testing.T) {
	extractor := ExtractHeader("X-API-Key")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	
	req.Header.Set("X-API-Key", "my-secret-key")
	if key := extractor(req); key != "my-secret-key" {
		t.Errorf("Expected my-secret-key, got %s", key)
	}
}

func TestExtractCookie(t *testing.T) {
	extractor := ExtractCookie("session_id")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "cookie-val"})
	if key := extractor(req); key != "cookie-val" {
		t.Errorf("Expected cookie-val, got %s", key)
	}

	// Missing cookie
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	if key := extractor(req2); key != "" {
		t.Errorf("Expected empty string, got %s", key)
	}
}

func TestMiddleware_Options(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	lb := New(client, 10.0)

	var customErrorCalled, onLimitCalled bool

	errorHandler := WithErrorHandler(func(w http.ResponseWriter, r *http.Request, res *Result) {
		customErrorCalled = true
		w.WriteHeader(http.StatusPaymentRequired)
	})

	onLimitHandler := WithOnLimit(func(r *http.Request, res *Result) {
		onLimitCalled = true
	})

	mw := Middleware(lb, func(r *http.Request) string { return "test" }, errorHandler, onLimitHandler)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	
	// First is OK
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	// Second is rate limited
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusPaymentRequired {
		t.Errorf("Expected custom error code 402, got %d", rec2.Code)
	}

	if !customErrorCalled {
		t.Error("Custom error handler was not called")
	}

	if !onLimitCalled {
		t.Error("OnLimit handler was not called")
	}
}

func TestMiddleware_FailOpen(t *testing.T) {
	client := createTestClient(t)
	client.Close() // Force fail

	lb := New(client, 10.0)
	mw := Middleware(lb, func(r *http.Request) string { return "fail" })
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should fail open
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}
