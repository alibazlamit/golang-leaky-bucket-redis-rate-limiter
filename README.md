# Leaky Bucket Redis

A distributed rate limiting implementation using the Leaky Bucket algorithm with Redis as the backend. Designed for high-performance, distributed systems that need reliable rate limiting across multiple instances.

## Features

- **Distributed**: Works seamlessly across multiple application instances
- **High Precision**: Sub-second accuracy using nanosecond timestamps
- **Atomic Operations**: Uses Redis Lua scripts for consistency
- **Context-Aware**: Supports Go context for cancellation and timeouts
- **Production Ready**: Comprehensive test coverage and error handling
- **Zero Dependencies**: Only requires Redis and go-redis client

## Installation

```bash
go get github.com/alibazlamit/leaky_bucket_redis
```

**Requirements:**
- Go 1.21 or higher
- Redis 6.0 or higher

## Quick Start

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
    // Initialize Redis client
    client := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    defer client.Close()

    // Create rate limiter: 10 requests per second
    limiter := leaky_bucket.NewLeakyBucket(client, "my_rate_limit", 10.0)

    // Check if request is allowed
    ctx := context.Background()
    waitTime := limiter.Allow(ctx)

    if waitTime > 0 {
        fmt.Printf("Rate limited. Retry after %.2f seconds\n", waitTime.Seconds())
    } else {
        fmt.Println("Request allowed!")
    }
}
```

## API Reference

### `NewLeakyBucket(client *redis.Client, key string, rate float64) *LeakyBucketRedis`

Creates a new rate limiter instance.

**Parameters:**
- `client`: Redis client connection
- `key`: Unique identifier for this rate limiter (e.g., "api_limit", "user:123")
- `rate`: Requests per second (e.g., 10.0 = 10 requests/second)

**Returns:**
- `*LeakyBucketRedis`: Rate limiter instance, or `nil` if parameters are invalid

### `Allow(ctx context.Context) time.Duration`

Checks if a request should be allowed based on the rate limit.

**Parameters:**
- `ctx`: Context for cancellation and timeout

**Returns:**
- `time.Duration`: 
  - `0` if request is allowed
  - `> 0` indicating how long to wait before retrying

**Behavior:**
- Returns immediately with wait time if rate limited
- Automatically fails open on Redis errors (allows request)
- Thread-safe and supports concurrent requests

## Usage Examples

### HTTP API Rate Limiting

```go
http.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
    limiter := leaky_bucket.NewLeakyBucket(client, "api_rate_limit", 10.0)
    waitTime := limiter.Allow(r.Context())

    if waitTime > 0 {
        w.Header().Set("Retry-After", fmt.Sprintf("%.0f", waitTime.Seconds()))
        w.WriteHeader(http.StatusTooManyRequests)
        fmt.Fprintf(w, "Rate limit exceeded. Retry after %.2f seconds\n", waitTime.Seconds())
        return
    }

    // Process request
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Success!\n")
})
```

### Per-User Rate Limiting

```go
func rateLimitUser(userID string, client *redis.Client) bool {
    key := fmt.Sprintf("user_rate_limit:%s", userID)
    limiter := leaky_bucket.NewLeakyBucket(client, key, 5.0) // 5 req/sec per user
    
    ctx := context.Background()
    waitTime := limiter.Allow(ctx)
    
    return waitTime == 0 // true if allowed
}
```

### Batch Processing with Rate Limiting

```go
func processBatch(items []string, client *redis.Client) {
    limiter := leaky_bucket.NewLeakyBucket(client, "batch_api", 2.0) // 2 req/sec
    
    for _, item := range items {
        ctx := context.Background()
        waitTime := limiter.Allow(ctx)
        
        if waitTime > 0 {
            time.Sleep(waitTime) // Wait before processing
        }
        
        // Process item (e.g., call external API)
        processItem(item)
    }
}
```

## How It Works

The Leaky Bucket algorithm controls the rate of requests by:

1. **Token Tracking**: Each allowed request adds a "token" with a timestamp to a Redis sorted set
2. **Rate Calculation**: The bucket "leaks" at a constant rate (your specified requests/second)
3. **Cleanup**: Old tokens beyond the bucket capacity are automatically removed
4. **Wait Time**: If bucket is full, calculates exact wait time until a token can be added

**Key advantages:**
- Smooths burst traffic into steady flow
- Distributed across multiple servers via Redis
- Atomic operations prevent race conditions
- Sub-second precision for accurate rate limiting

## Running Examples

The `examples/` directory contains complete working examples:

```bash
# HTTP API rate limiting
go run examples/http_api.go

# Per-user rate limiting
go run examples/per_user_limiting.go

# Batch processing with rate limiting
go run examples/batch_processing.go
```

## Testing

Run the comprehensive test suite:

```bash
# Run all tests
go test ./leaky_bucket -v

# Run with coverage
go test ./leaky_bucket -cover

# Run benchmarks
go test ./leaky_bucket -bench=.
```

**Test Coverage:**
- Basic allow/deny behavior
- Rate limit enforcement
- Concurrent request handling
- Token refill over time
- Invalid configuration handling
- Context cancellation
- Different rate configurations

## Performance

Benchmarks on standard hardware:

```
BenchmarkLeakyBucketRedis_Allow-8        10000    ~150 µs/op
BenchmarkLeakyBucketRedis_Concurrent-8    5000    ~300 µs/op
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT License - see LICENSE file for details

---

If you find this implementation useful, feel free to drop a star! ⭐
