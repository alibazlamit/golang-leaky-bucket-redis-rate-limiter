// Batch processing with rate limiting example
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	leaky_bucket "github.com/alibazlamit/leaky_bucket_redis/leaky_bucket"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Initialize Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	// Test Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Create rate limiter: 2 API calls per second to external service
	rateLimiter := leaky_bucket.NewLeakyBucket(client, "external_api_batch", 2.0)

	// Simulate batch processing with rate limiting
	items := []string{"item1", "item2", "item3", "item4", "item5", "item6", "item7", "item8"}

	fmt.Printf("Processing %d items with rate limit of 2 per second\n\n", len(items))
	startTime := time.Now()

	for i, item := range items {
		ctx := context.Background()
		waitTime := rateLimiter.Allow(ctx)

		if waitTime > 0 {
			fmt.Printf("[%d] Rate limited. Waiting %.2f seconds before processing %s\n",
				i+1, waitTime.Seconds(), item)
			time.Sleep(waitTime)

			// Try again after waiting
			waitTime = rateLimiter.Allow(ctx)
			if waitTime > 0 {
				fmt.Printf("[%d] Still rate limited after waiting, skipping %s\n", i+1, item)
				continue
			}
		}

		// Process the item (simulated API call)
		fmt.Printf("[%d] Processing %s at %v\n",
			i+1, item, time.Now().Format("15:04:05.000"))

		// Simulate processing time
		time.Sleep(50 * time.Millisecond)
	}

	duration := time.Since(startTime)
	fmt.Printf("\nCompleted processing in %.2f seconds\n", duration.Seconds())
	fmt.Printf("Average rate: %.2f items/second\n", float64(len(items))/duration.Seconds())
}
