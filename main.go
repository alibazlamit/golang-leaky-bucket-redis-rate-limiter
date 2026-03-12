package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	leaky_bucket "github.com/alibazlamit/leaky_bucket_redis/v2/leaky_bucket"
	"github.com/redis/go-redis/v9"
)

func main() {
	fmt.Println("=== Leaky Bucket Redis Demo ===")

	// Initialize Redis client
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
	defer client.Close()

	// Test Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v\n", err)
	}
	fmt.Println("✓ Connected to Redis")

	// Clean up any existing test data
	client.FlushDB(context.Background())

	// Demo 1: Basic rate limiting with Burst
	fmt.Println("\n--- Demo 1: Basic Rate Limiting (5 req/sec, Burst 2) ---")
	basicDemo(client)

	// Demo 2: Concurrent requests
	fmt.Println("\n--- Demo 2: Concurrent Requests (10 req/sec) ---")
	concurrentDemo(client)

	// Demo 3: Sequential processing with Wait()
	fmt.Println("\n--- Demo 3: Sequential Processing with Wait() ---")
	sequentialDemo(client)

	fmt.Println("\n=== Demo Complete ===")
}

func basicDemo(client redis.UniversalClient) {
	// Create rate limiter: 5 requests per second, burst of 2
	limiter := leaky_bucket.New(client, 5.0, leaky_bucket.WithBurst(2))
	fmt.Println("Rate limit: 5 requests/second, Burst: 2")

	ctx := context.Background()
	key := "demo_basic"
	allowed := 0
	denied := 0

	// Try 10 requests immediately
	for i := 1; i <= 10; i++ {
		res, err := limiter.Allow(ctx, key)
		if err != nil {
			log.Printf("Error: %v\n", err)
			continue
		}

		if res.Allowed {
			allowed++
			fmt.Printf("  Request %d: ✓ Allowed (Remaining: %d)\n", i, res.Remaining)
		} else {
			denied++
			fmt.Printf("  Request %d: ✗ Denied (wait %.3fs)\n", i, res.WaitTime.Seconds())
		}
	}

	fmt.Printf("Result: %d allowed, %d denied\n", allowed, denied)
}

func concurrentDemo(client redis.UniversalClient) {
	// Create rate limiter: 10 requests per second
	limiter := leaky_bucket.New(client, 10.0)
	fmt.Println("Rate limit: 10 requests/second")
	fmt.Println("Sending 20 concurrent requests...")

	startTime := time.Now()
	numRequests := 20
	key := "demo_concurrent"

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	denied := 0

	for i := 1; i <= numRequests; i++ {
		wg.Add(1)
		go func(requestNum int) {
			defer wg.Done()

			ctx := context.Background()
			res, err := limiter.Allow(ctx, key)
			if err != nil {
				return
			}

			mu.Lock()
			if res.Allowed {
				allowed++
			} else {
				denied++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	fmt.Printf("Result: %d allowed, %d denied in %.3fs\n", allowed, denied, duration.Seconds())
}

func sequentialDemo(client redis.UniversalClient) {
	// Create rate limiter: 2 requests per second
	limiter := leaky_bucket.New(client, 2.0)
	fmt.Println("Rate limit: 2 requests/second")
	fmt.Println("Processing 5 requests with automatic waiting...")

	startTime := time.Now()
	ctx := context.Background()
	key := "demo_sequential"

	for i := 1; i <= 5; i++ {
		requestStart := time.Now()
		
		// Use the new Wait() method
		err := limiter.Wait(ctx, key)
		if err != nil {
			fmt.Printf("  Request %d: Error: %v\n", i, err)
			continue
		}

		elapsed := time.Since(requestStart)
		fmt.Printf("  Request %d: ✓ Processed (took %.3fs)\n", i, elapsed.Seconds())
	}

	totalDuration := time.Since(startTime)
	fmt.Printf("Total time: %.3fs (expected ~2.5s for 5 requests at 2/sec)\n", totalDuration.Seconds())
}
