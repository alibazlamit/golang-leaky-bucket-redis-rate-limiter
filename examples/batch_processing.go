//go:build ignore

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

	// 1. Create rate limiter: 2 API calls per second
	limiter := leaky_bucket.New(client, 2.0)

	// Simulate batch processing
	items := []string{"item1", "item2", "item3", "item4", "item5"}
	key := "batch_job_123"

	fmt.Printf("Processing %d items with rate limit of 2 per second using Wait()\n\n", len(items))
	startTime := time.Now()

	for i, item := range items {
		// 2. Use Wait() to automatically block until the rate limit allows
		err := limiter.Wait(context.Background(), key)
		if err != nil {
			log.Printf("Wait error: %v\n", err)
			continue
		}

		// Process the item
		fmt.Printf("[%d] Processing %s at %v\n",
			i+1, item, time.Now().Format("15:04:05.000"))
	}

	duration := time.Since(startTime)
	fmt.Printf("\nCompleted in %.2f seconds\n", duration.Seconds())
}
