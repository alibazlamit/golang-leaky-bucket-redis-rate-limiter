package leaky_bucket_redis

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Helper function to create an in-memory test Redis client
func createTestClient(t *testing.T) redis.UniversalClient {
	s := miniredis.RunT(t)
	
	client := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})

	return client
}

func TestLeakyBucketRedis_BasicAllow(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_basic"
	rate := 10.0 // 10 requests per second
	lb := New(client, rate)

	ctx := context.Background()

	// First request should be allowed immediately
	res, err := lb.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Errorf("Expected first request to be allowed")
	}
	if res.WaitTime != 0 {
		t.Errorf("Expected wait time 0, got %v", res.WaitTime)
	}

	// Second request should be rate limited (need to wait ~100ms for 10 req/s)
	res, err = lb.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Allowed {
		t.Error("Expected second request to be rate limited (Burst=1)")
	}

	expectedWait := time.Second / time.Duration(rate)
	tolerance := 50 * time.Millisecond
	if res.WaitTime < expectedWait-tolerance || res.WaitTime > expectedWait+tolerance {
		t.Errorf("Expected wait time around %v, got %v", expectedWait, res.WaitTime)
	}
}

func TestLeakyBucketRedis_Burst(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_burst"
	rate := 10.0
	burst := 3
	lb := New(client, rate, WithBurst(burst))

	ctx := context.Background()

	// 3 requests should be allowed immediately
	for i := 0; i < burst; i++ {
		res, _ := lb.Allow(ctx, key)
		if !res.Allowed {
			t.Errorf("Request %d should be allowed with burst %d", i+1, burst)
		}
	}

	// 4th request should be denied
	res, _ := lb.Allow(ctx, key)
	if res.Allowed {
		t.Error("4th request should be denied")
	}
}

func TestLeakyBucketRedis_Wait(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_wait"
	rate := 5.0 // 200ms interval
	lb := New(client, rate)

	ctx := context.Background()

	// Consume first token
	lb.Allow(ctx, key)

	start := time.Now()
	err := lb.Wait(ctx, key)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	expectedWait := 200 * time.Millisecond
	if elapsed < expectedWait-50*time.Millisecond {
		t.Errorf("Wait returned too early: %v", elapsed)
	}
}

func TestLeakyBucketRedis_WaitTimeout(t *testing.T) {
	client := createTestClient(t)
	// Rate of 1 per hour (practically blocked)
	lb := New(client, 1.0/3600.0, WithBurst(0))
	key := "test_wait_timeout"

	// First one allowed
	_, err := lb.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// WaitTimeout with 100ms should fail
	start := time.Now()
	err = lb.WaitTimeout(context.Background(), key, 100*time.Millisecond)
	duration := time.Since(start)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if duration < 100*time.Millisecond {
		t.Errorf("WaitTimeout returned too early: %v", duration)
	}
}

func TestLeakyBucketRedis_Concurrent(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_concurrent"
	rate := 20.0
	lb := New(client, rate, WithBurst(5))

	ctx := context.Background()

	numGoroutines := 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowedCount := 0
	deniedCount := 0

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := lb.Allow(ctx, key)
			if err != nil {
				return
			}
			mu.Lock()
			if res.Allowed {
				allowedCount++
			} else {
				deniedCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Should have exactly 5 allowed (due to burst) if they all arrived simultaneously
	// but some might be slightly delayed. At least 5 should be allowed.
	if allowedCount < 5 {
		t.Errorf("Expected at least 5 allowed requests, got %d", allowedCount)
	}

	t.Logf("Concurrent test: %d allowed, %d denied", allowedCount, deniedCount)
}

func TestMiddleware(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	rate := 10.0
	lb := New(client, rate)

	mw := Middleware(lb, func(r *http.Request) string {
		return "test_user"
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request - OK
	req1 := httptest.NewRequest("GET", "/", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr1.Code)
	}

	// Second request - Rate Limited
	req2 := httptest.NewRequest("GET", "/", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", rr2.Code)
	}
}

func TestLeakyBucketRedis_ContextCancellation(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_context"
	lb := New(client, 10.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should handle cancelled context (Redis call might fail, we fail open)
	res, err := lb.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("Expected to fail open on cancelled context if it caused an error")
	}
}

func BenchmarkLeakyBucketRedis_Allow(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis not available: %v", err)
	}

	lb := New(client, 10000.0, WithBurst(10000))
	client.FlushDB(ctx)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lb.Allow(ctx, "bench")
		}
	})
}
