//go:build ignore

// Simple HTTP API rate limiting example
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	leaky_bucket "github.com/alibazlamit/leaky_bucket_redis/v2/leaky_bucket"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Initialize Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	// 1. Create rate limiter: 10 requests per second with burst of 5
	limiter := leaky_bucket.New(client, 10.0, leaky_bucket.WithBurst(5))

	// 2. Create middleware using client IP as key
	mw := leaky_bucket.Middleware(limiter, leaky_bucket.ExtractIP)

	// 3. Apply middleware to your handler
	mux := http.NewServeMux()
	mux.Handle("/api/data", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Request processed successfully at %v\n", time.Now().Format(time.RFC3339))
	})))

	fmt.Println("API server starting on :8080")
	fmt.Println("Rate limit: 10 requests per second, Burst: 5")
	fmt.Println("Try: curl -i http://localhost:8080/api/data")

	log.Fatal(http.ListenAndServe(":8080", mux))
}
