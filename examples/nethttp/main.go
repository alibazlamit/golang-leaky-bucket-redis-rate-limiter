package main

import (
	"log"
	"net/http"
	"time"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/redis/go-redis/v9"
)

func main() {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	// 1 request per second per client IP, with bursts of 5.
	l, err := limiter.New(client, 1, limiter.WithBurst(5))
	if err != nil {
		log.Fatal(err)
	}

	hello := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello\n"))
	})

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           limiter.Middleware(l, limiter.ClientIP())(hello),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
