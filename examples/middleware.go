// HTTP middleware pattern example
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

// RateLimitMiddleware creates an HTTP middleware for rate limiting
func RateLimitMiddleware(client *redis.Client, rate float64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Use client IP as the rate limit key
			clientIP := r.RemoteAddr
			key := fmt.Sprintf("rate_limit:%s", clientIP)

			limiter := leaky_bucket.NewLeakyBucket(client, key, rate)
			waitTime := limiter.Allow(r.Context())

			if waitTime > 0 {
				// Rate limited
				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rate))
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", waitTime.Seconds()))
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, "Rate limit exceeded. Please try again in %.2f seconds.\n",
					waitTime.Seconds())
				return
			}

			// Request allowed - proceed to next handler
			next.ServeHTTP(w, r)
		})
	}
}

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

	// Create HTTP mux
	mux := http.NewServeMux()

	// Define handlers
	mux.HandleFunc("/api/public", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Public endpoint - no rate limit\n")
	})

	mux.HandleFunc("/api/protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Protected endpoint - rate limited to 5 req/sec\nRequest processed at: %v\n",
			time.Now().Format(time.RFC3339))
	})

	// Apply rate limiting middleware only to protected route
	http.Handle("/api/public", mux)
	http.Handle("/api/protected", RateLimitMiddleware(client, 5.0)(mux))

	fmt.Println("Server starting on :8080")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  - GET /api/public    (no rate limit)")
	fmt.Println("  - GET /api/protected (5 requests/second per IP)")
	fmt.Println()
	fmt.Println("Try running:")
	fmt.Println("  curl http://localhost:8080/api/public")
	fmt.Println("  for i in {1..10}; do curl http://localhost:8080/api/protected; done")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
