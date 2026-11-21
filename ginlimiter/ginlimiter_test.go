package ginlimiter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func setup(t *testing.T, opts ...limiter.Option) (*miniredis.Miniredis, *limiter.Limiter) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	l, err := limiter.New(client, 1, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return srv, l
}

func serve(mw gin.HandlerFunc, apiKey string) (*httptest.ResponseRecorder, *int) {
	reached := new(int)
	r := gin.New()
	r.Use(mw)
	r.GET("/", func(c *gin.Context) { *reached++; c.Status(http.StatusOK) })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if apiKey != "" {
		req.Header.Set("X-User", apiKey)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec, reached
}

func TestMiddleware(t *testing.T) {
	_, l := setup(t)
	mw := Middleware(l, byUser)

	if rec, _ := serve(mw, "abc"); rec.Code != http.StatusOK || rec.Header().Get("X-RateLimit-Limit") != "1" {
		t.Fatalf("first: %d %v", rec.Code, rec.Header())
	}
	rec, reached := serve(mw, "abc")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" || *reached != 0 {
		t.Fatalf("second: %d %v reached=%d", rec.Code, rec.Header(), *reached)
	}
	if rec, reached := serve(mw, ""); rec.Code != http.StatusBadRequest || *reached != 0 {
		t.Fatalf("no key: %d reached=%d", rec.Code, *reached)
	}
}

func TestDenyHandlerIsUsedAndAlwaysAborts(t *testing.T) {
	_, l := setup(t)
	mw := Middleware(l, byUser,
		WithDenyHandler(func(c *gin.Context, status int, _ limiter.Result) {
			c.String(http.StatusTeapot, "%d", status)
		}),
	)

	serve(mw, "abc")
	rec, reached := serve(mw, "abc")
	if rec.Code != http.StatusTeapot || rec.Body.String() != "429" || *reached != 0 {
		t.Fatalf("got %d %q reached=%d", rec.Code, rec.Body.String(), *reached)
	}
}

func TestRedisDown(t *testing.T) {
	srv, closed := setup(t)
	srv.Close()
	var reported int
	onError := WithOnError(func(*gin.Context, error) { reported++ })

	if rec, _ := serve(Middleware(closed, byUser, onError), "abc"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("default: got %d, want 503", rec.Code)
	}

	srv, open := setup(t, limiter.WithFailOpen())
	srv.Close()
	if rec, reached := serve(Middleware(open, byUser, onError), "abc"); rec.Code != http.StatusOK || *reached != 1 {
		t.Fatalf("fail open: got %d reached=%d", rec.Code, *reached)
	}
	if reported != 2 {
		t.Fatalf("OnError ran %d times, want 2", reported)
	}
}

var byUser limiter.KeyFunc = func(r *http.Request) string { return r.Header.Get("X-User") }
