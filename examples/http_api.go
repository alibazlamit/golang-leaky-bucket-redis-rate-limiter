// Simple HTTP API rate limiting example
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
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

	// Create rate limiter: 10 requests per second
	rateLimiter := leaky_bucket.NewLeakyBucket(client, "api_rate_limit", 10.0)

	// HTTP handler with rate limiting
	http.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		waitTime := rateLimiter.Allow(r.Context())

		if waitTime > 0 {
			// Rate limited - return 429
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", waitTime.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, "Rate limit exceeded. Retry after %.2f seconds\n", waitTime.Seconds())
			return
		}

		// Request allowed
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Request processed successfully at %v\n", time.Now().Format(time.RFC3339))
	})

	fmt.Println("API server starting on :8080")
	fmt.Println("Rate limit: 10 requests per second")
	fmt.Println("Try: curl http://localhost:8080/api/data")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
