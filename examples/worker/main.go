package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	// 2 jobs per second, shared by every worker process that uses this key.
	l, err := limiter.New(client, 2)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	for job := 1; job <= 6; job++ {
		if err := l.Wait(ctx, "email-provider"); err != nil {
			return err
		}
		fmt.Printf("job %d sent at +%v\n", job, time.Since(start).Round(100*time.Millisecond))
	}
	return nil
}
