package main

import (
	"fmt"
	"log"
	"net/http"

	leaky_bucket "github.com/alibazlamit/leaky_bucket_redis/leaky_bucket"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Initialize Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	// 1. Create rate limiter: 5 requests per second
	limiter := leaky_bucket.New(client, 5.0)

	// 2. Wrap the whole mux with the middleware, or apply to specific routes
	mux := http.NewServeMux()

	// Public endpoint - no rate limit on this specific mux
	mux.HandleFunc("/api/public", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Public endpoint\n")
	})

	// Protected endpoint - apply middleware to it
	mw := leaky_bucket.Middleware(limiter, leaky_bucket.ExtractIP)
	
	mux.Handle("/api/protected", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Protected endpoint - 5 req/sec\n")
	})))

	fmt.Println("Server starting on :8080")
	fmt.Println("Try: curl http://localhost:8080/api/protected")

	log.Fatal(http.ListenAndServe(":8080", mux))
}
