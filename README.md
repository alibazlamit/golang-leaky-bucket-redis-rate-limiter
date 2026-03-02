# Go Leaky Bucket Rate Limiter with Redis

[![Run Tests](https://github.com/alibazlamit/leaky_bucket_redis/actions/workflows/test.yml/badge.svg)](https://github.com/alibazlamit/leaky_bucket_redis/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/alibazlamit/leaky_bucket_redis)](https://goreportcard.com/report/github.com/alibazlamit/leaky_bucket_redis)
[![Go Reference](https://pkg.go.dev/badge/github.com/alibazlamit/leaky_bucket_redis.svg)](https://pkg.go.dev/github.com/alibazlamit/leaky_bucket_redis)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A high-performance **distributed rate limiter in Go** built using the **Leaky Bucket algorithm** and **Redis** as the backend.  
Ideal for APIs, microservices, and distributed systems that need **accurate request throttling** across multiple instances.

⭐ **If you find this library useful, please consider giving it a star! It helps others discover this project.** ⭐

---

## Why Use This Library?

While there are several rate limiters available for Go, this library specifically implements a strict **Leaky Bucket** algorithm via highly optimized atomic Lua scripts in Redis. This provides:

- **Exact Smooth Throttling:** Token buckets allow bursts; leaky buckets strictly smooth out requests to a constant, predictable rate.
- **Zero-Dependency Core:** Only depends on the official `go-redis` client, keeping your modules clean.
- **Sub-Second Precision:** Millisecond/nanosecond resolution ensures perfect timing between cross-server requests.
- **Fail-Open Fallback:** Designed so that if your Redis node suddenly becomes unreachable, it won't indefinitely block or take down your API.

---

## Features

- **Redis-Backed Distributed Limiting** – works across multiple Go servers
- **Precise Timing** – sub-second accuracy via nanosecond timestamps  
- **Atomic Lua Scripts** – ensures consistent rate control in Redis  
- **Context-Aware** – supports `context.Context` for cancellation & timeouts  
- **Production-Ready** – robust error handling and full test coverage  
- **Lightweight** – no dependencies beyond Go and Redis  

---

## Installation

```bash
go get github.com/alibazlamit/leaky_bucket_redis
```

**Requirements:**
- Go 1.21+
- Redis 6.0+

---

## Quick Example (Leaky Bucket + Redis)

```go
package main

import (
    "context"
    "fmt"
    "time"

    leaky_bucket "github.com/alibazlamit/leaky_bucket_redis/leaky_bucket"
    "github.com/redis/go-redis/v9"
)

func main() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    limiter := leaky_bucket.NewLeakyBucket(client, "api_limit", 10.0) // 10 req/sec
    ctx := context.Background()

    wait := limiter.Allow(ctx)
    if wait > 0 {
        fmt.Printf("Rate limited. Retry after %.2f seconds\n", wait.Seconds())
        return
    }

    fmt.Println("Request allowed!")
}
```

---

## API Reference

### `NewLeakyBucket(client *redis.Client, key string, rate float64) *LeakyBucketRedis`

Creates a new **Redis-based rate limiter** using the Leaky Bucket algorithm.

| Parameter | Type | Description |
|------------|------|-------------|
| `client` | *redis.Client | Active Redis connection |
| `key` | string | Unique key for rate limit scope (e.g. `"user:123"`) |
| `rate` | float64 | Allowed requests per second |

Returns: `*LeakyBucketRedis`

---

### `Allow(ctx context.Context) time.Duration`

Checks if a request is allowed and returns the **wait time** before retrying.

- Returns `0` → request allowed  
- Returns `>0` → number of seconds to wait before next attempt  

Behavior:
- Thread-safe  
- Atomic Redis operations  
- Fails open if Redis unavailable (never blocks)  

---

## Use Cases

### 1. HTTP API Rate Limiting

```go
limiter := leaky_bucket.NewLeakyBucket(client, "api_global", 10.0)
wait := limiter.Allow(r.Context())
```

### 2. Per-User Request Throttling
```go
key := fmt.Sprintf("user_limit:%s", userID)
limiter := leaky_bucket.NewLeakyBucket(client, key, 5.0)
```

### 3. Background Jobs / Batch Tasks
```go
limiter := leaky_bucket.NewLeakyBucket(client, "batch_limit", 2.0)
```

---

## ⚙️ How the Leaky Bucket Algorithm Works

The **Leaky Bucket algorithm** is a classic rate-limiting technique.  
This Go implementation with Redis provides **distributed consistency** and **precise control**.

1. Each incoming request adds a “token” to a Redis sorted set.  
2. Tokens “leak” at a steady rate based on your configured limit.  
3. When the bucket is full, new requests must wait until older tokens expire.  
4. Wait time = exact time until a new token can be added.  

Smooths bursts into a steady flow  
Works across multiple servers  
Atomic and thread-safe  

---

## Performance

| Benchmark | Ops/sec | Avg Time |
|------------|----------|-----------|
| `Allow` | 10,000 | ~150 µs/op |
| `Concurrent` | 5,000 | ~300 µs/op |

---

## Testing

```bash
go test ./leaky_bucket -v
go test ./leaky_bucket -cover
go test ./leaky_bucket -bench=.
```

Tests cover:
- Allow/Deny behavior  
- Redis failure tolerance  
- Context cancellation  
- Concurrency safety  

---

## Examples

In `examples/` directory:
- `http_api.go` → API rate limiting  
- `per_user_limiting.go` → user-based limits  
- `batch_processing.go` → throttled batch jobs  

Run an example:
```bash
go run examples/http_api.go
```

---

## 🤝 Contributing

Pull requests and issues are highly welcome! We'd love to have your contributions.

---

## 📄 License

MIT License – see LICENSE for details
