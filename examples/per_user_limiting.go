// Per-user rate limiting example
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

	// HTTP handler with per-user rate limiting
	http.HandleFunc("/api/user/data", func(w http.ResponseWriter, r *http.Request) {
		// Get user ID from request (in real app, from auth token)
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			http.Error(w, "user_id parameter required", http.StatusBadRequest)
			return
		}

		// Create rate limiter for this specific user: 5 requests per second
		key := fmt.Sprintf("user_rate_limit:%s", userID)
		rateLimiter := leaky_bucket.NewLeakyBucket(client, key, 5.0)

		waitTime := rateLimiter.Allow(r.Context())

		if waitTime > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", waitTime.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, "Rate limit exceeded for user %s. Retry after %.2f seconds\n",
				userID, waitTime.Seconds())
			return
		}

		// Request allowed
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "User %s: Request processed at %v\n",
			userID, time.Now().Format(time.RFC3339))
	})

	fmt.Println("Per-user API server starting on :8080")
	fmt.Println("Rate limit: 5 requests per second per user")
	fmt.Println("Try: curl 'http://localhost:8080/api/user/data?user_id=user123'")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
