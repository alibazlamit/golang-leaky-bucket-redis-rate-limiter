package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	leaky_bucket "github.com/alibazlamit/leaky_bucket_redis/leaky_bucket"
	"github.com/redis/go-redis/v9"
)

func main() {
	fmt.Println("=== Leaky Bucket Redis Demo ===\n")

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

	// Demo 1: Basic rate limiting
	fmt.Println("\n--- Demo 1: Basic Rate Limiting ---")
	basicDemo(client)

	// Demo 2: Concurrent requests
	fmt.Println("\n--- Demo 2: Concurrent Requests ---")
	concurrentDemo(client)

	// Demo 3: Sequential processing with wait
	fmt.Println("\n--- Demo 3: Sequential Processing ---")
	sequentialDemo(client)

	fmt.Println("\n=== Demo Complete ===")
}

func basicDemo(client *redis.Client) {
	// Create rate limiter: 5 requests per second
	limiter := leaky_bucket.NewLeakyBucket(client, "demo_basic", 5.0)
	fmt.Println("Rate limit: 5 requests/second")

	ctx := context.Background()
	allowed := 0
	denied := 0

	// Try 10 requests immediately
	for i := 1; i <= 10; i++ {
		waitTime := limiter.Allow(ctx)
		if waitTime == 0 {
			allowed++
			fmt.Printf("  Request %d: ✓ Allowed\n", i)
		} else {
			denied++
			fmt.Printf("  Request %d: ✗ Denied (wait %.3fs)\n", i, waitTime.Seconds())
		}
	}

	fmt.Printf("Result: %d allowed, %d denied\n", allowed, denied)
}

func concurrentDemo(client *redis.Client) {
	// Create rate limiter: 10 requests per second
	limiter := leaky_bucket.NewLeakyBucket(client, "demo_concurrent", 10.0)
	fmt.Println("Rate limit: 10 requests/second")
	fmt.Println("Sending 20 concurrent requests...")

	startTime := time.Now()
	numRequests := 20

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	denied := 0

	for i := 1; i <= numRequests; i++ {
		wg.Add(1)
		go func(requestNum int) {
			defer wg.Done()

			ctx := context.Background()
			waitTime := limiter.Allow(ctx)

			mu.Lock()
			if waitTime == 0 {
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

func sequentialDemo(client *redis.Client) {
	// Create rate limiter: 2 requests per second
	limiter := leaky_bucket.NewLeakyBucket(client, "demo_sequential", 2.0)
	fmt.Println("Rate limit: 2 requests/second")
	fmt.Println("Processing 5 requests with automatic waiting...")

	startTime := time.Now()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		requestStart := time.Now()
		waitTime := limiter.Allow(ctx)

		if waitTime > 0 {
			fmt.Printf("  Request %d: Waiting %.3fs...\n", i, waitTime.Seconds())
			time.Sleep(waitTime)

			// Try again after waiting
			waitTime = limiter.Allow(ctx)
		}

		elapsed := time.Since(requestStart)
		fmt.Printf("  Request %d: ✓ Processed (took %.3fs)\n", i, elapsed.Seconds())
	}

	totalDuration := time.Since(startTime)
	fmt.Printf("Total time: %.3fs (expected ~2.0s for 5 requests at 2/sec)\n", totalDuration.Seconds())
}
