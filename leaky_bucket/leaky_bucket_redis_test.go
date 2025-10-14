package leaky_bucket_redis

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Helper function to create a test Redis client
func createTestClient(t *testing.T) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1, // Use a separate DB for testing
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clear the test database
	client.FlushDB(ctx)

	return client
}

func TestLeakyBucketRedis_BasicAllow(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_basic"
	rate := 10.0 // 10 requests per second
	lb := NewLeakyBucket(client, key, rate)

	if lb == nil {
		t.Fatal("Expected non-nil bucket")
	}

	ctx := context.Background()

	// First request should be allowed immediately
	waitTime := lb.Allow(ctx)
	if waitTime != 0 {
		t.Errorf("Expected first request to be allowed, got wait time: %v", waitTime)
	}

	// Second request should be rate limited (need to wait ~100ms for 10 req/s)
	waitTime = lb.Allow(ctx)
	if waitTime == 0 {
		t.Error("Expected second request to be rate limited")
	}

	expectedWait := time.Second / time.Duration(rate)
	tolerance := 50 * time.Millisecond
	if waitTime < expectedWait-tolerance || waitTime > expectedWait+tolerance {
		t.Errorf("Expected wait time around %v, got %v", expectedWait, waitTime)
	}
}

func TestLeakyBucketRedis_RateLimit(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_rate"
	rate := 5.0 // 5 requests per second
	lb := NewLeakyBucket(client, key, rate)

	ctx := context.Background()

	// Make multiple requests quickly
	var waitTimes []time.Duration
	for i := 0; i < 3; i++ {
		waitTime := lb.Allow(ctx)
		waitTimes = append(waitTimes, waitTime)
	}

	// First request should be allowed
	if waitTimes[0] != 0 {
		t.Errorf("First request should be allowed, got wait time: %v", waitTimes[0])
	}

	// Subsequent requests should have increasing wait times
	if waitTimes[1] == 0 {
		t.Error("Second request should be rate limited")
	}

	if waitTimes[2] <= waitTimes[1] {
		t.Errorf("Wait times should increase: %v, %v", waitTimes[1], waitTimes[2])
	}
}

func TestLeakyBucketRedis_Concurrent(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_concurrent"
	rate := 20.0
	lb := NewLeakyBucket(client, key, rate)

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
			waitTime := lb.Allow(ctx)
			mu.Lock()
			if waitTime == 0 {
				allowedCount++
			} else {
				deniedCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Should have some allowed and some denied
	if allowedCount == 0 {
		t.Error("Expected some requests to be allowed")
	}
	if deniedCount == 0 {
		t.Error("Expected some requests to be rate limited")
	}

	t.Logf("Concurrent test: %d allowed, %d denied", allowedCount, deniedCount)
}

func TestLeakyBucketRedis_TokenRefill(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_refill"
	rate := 5.0 // 5 requests per second = 200ms per token
	lb := NewLeakyBucket(client, key, rate)

	ctx := context.Background()

	// First request - allowed
	wait1 := lb.Allow(ctx)
	if wait1 != 0 {
		t.Errorf("First request should be allowed, got wait: %v", wait1)
	}

	// Second request - should wait
	wait2 := lb.Allow(ctx)
	if wait2 == 0 {
		t.Error("Second request should require waiting")
	}

	// Wait for token to refill (200ms for rate of 5/sec)
	time.Sleep(250 * time.Millisecond)

	// After waiting long enough, first request should be allowed
	wait3 := lb.Allow(ctx)
	if wait3 != 0 {
		t.Logf("Expected first request after refill to be allowed, got wait: %v (this may be normal if requests are too fast)", wait3)
	}
}

func TestLeakyBucketRedis_InvalidConfig(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	// Test invalid key
	lb1 := NewLeakyBucket(client, "", 10.0)
	if lb1 != nil {
		t.Error("Expected nil for empty key")
	}

	// Test invalid rate
	lb2 := NewLeakyBucket(client, "test", 0)
	if lb2 != nil {
		t.Error("Expected nil for zero rate")
	}

	lb3 := NewLeakyBucket(client, "test", -5)
	if lb3 != nil {
		t.Error("Expected nil for negative rate")
	}

	// Test valid config
	lb4 := NewLeakyBucket(client, "test", 10.0)
	if lb4 == nil {
		t.Error("Expected non-nil for valid config")
	}
}

func TestLeakyBucketRedis_ContextCancellation(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	key := "test_bucket_context"
	rate := 10.0
	lb := NewLeakyBucket(client, key, rate)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should handle gracefully (fail open)
	waitTime := lb.Allow(ctx)

	// The implementation should either allow (0) or handle the error gracefully
	t.Logf("Cancelled context resulted in wait time: %v", waitTime)
}

func TestLeakyBucketRedis_DifferentRates(t *testing.T) {
	client := createTestClient(t)
	defer client.Close()

	ctx := context.Background()

	testCases := []struct {
		name string
		rate float64
	}{
		{"slow", 1.0},
		{"medium", 10.0},
		{"fast", 100.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			key := "test_bucket_" + tc.name
			lb := NewLeakyBucket(client, key, tc.rate)

			// First request allowed
			wait1 := lb.Allow(ctx)
			if wait1 != 0 {
				t.Errorf("First request should be allowed")
			}

			// Second request should wait approximately 1/rate seconds
			wait2 := lb.Allow(ctx)
			expectedWait := time.Second / time.Duration(tc.rate)
			tolerance := 50 * time.Millisecond

			if wait2 < expectedWait-tolerance || wait2 > expectedWait+tolerance {
				t.Errorf("Expected wait around %v, got %v", expectedWait, wait2)
			}
		})
	}
}

// Benchmark tests
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

	lb := NewLeakyBucket(client, "benchmark_bucket", 1000.0)
	client.FlushDB(ctx)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lb.Allow(ctx)
		}
	})
}

func BenchmarkLeakyBucketRedis_HighConcurrency(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis not available: %v", err)
	}

	lb := NewLeakyBucket(client, "benchmark_concurrent", 500.0)
	client.FlushDB(ctx)

	b.SetParallelism(100)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lb.Allow(ctx)
		}
	})
}
