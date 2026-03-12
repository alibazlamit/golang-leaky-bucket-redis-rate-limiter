//go:build ignore

// Per-user rate limiting example
package main

import (
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

	// 1. Create a single rate limiter instance for all users
	// This illustrates the "plug and play" efficiency of the new API.
	limiter := leaky_bucket.New(client, 5.0, leaky_bucket.WithBurst(3))

	// HTTP handler with per-user rate limiting
	http.HandleFunc("/api/user/data", func(w http.ResponseWriter, r *http.Request) {
		// Get user ID from request
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			http.Error(w, "user_id parameter required", http.StatusBadRequest)
			return
		}

		// 2. Use the same limiter instance with a user-specific key
		key := fmt.Sprintf("user_rate_limit:%s", userID)
		res, err := limiter.Allow(r.Context(), key)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if !res.Allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", res.WaitTime.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, "Rate limit exceeded for user %s. Retry after %.2f seconds\n",
				userID, res.WaitTime.Seconds())
			return
		}

		// Request allowed
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "User %s: Request processed at %v\n",
			userID, time.Now().Format(time.RFC3339))
	})

	fmt.Println("Per-user API server starting on :8080")
	fmt.Println("Rate limit: 5 requests per second per user (Burst: 3)")
	fmt.Println("Try: curl 'http://localhost:8080/api/user/data?user_id=user1' and '...user_id=user2'")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
