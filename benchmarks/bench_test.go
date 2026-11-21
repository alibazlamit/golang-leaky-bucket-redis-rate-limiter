package benchmarks

import (
	"context"
	"os"
	"testing"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

func client(b *testing.B) *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		b.Skip("set REDIS_ADDR, for example REDIS_ADDR=localhost:6379")
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	b.Cleanup(func() { c.Close() })
	return c
}

func BenchmarkThisLibrary(b *testing.B) {
	l, err := limiter.New(client(b), 1e6, limiter.WithBurst(1e6))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := l.Allow(ctx, "bench"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRedisRate(b *testing.B) {
	l := redis_rate.NewLimiter(client(b))
	limit := redis_rate.Limit{Rate: 1e6, Burst: 1e6, Period: 1e9}
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := l.Allow(ctx, "bench", limit); err != nil {
				b.Fatal(err)
			}
		}
	})
}
