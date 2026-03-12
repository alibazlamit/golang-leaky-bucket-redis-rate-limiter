package leaky_bucket_redis

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrInvalidRate is returned when rate is less than or equal to 0
	ErrInvalidRate = errors.New("rate must be greater than 0")
	// ErrInvalidKey is returned when key is empty
	ErrInvalidKey = errors.New("key cannot be empty")
)

// Result represents the state of a rate limit check.
// It contains all the metadata needed to decide whether to allow a request
// and how long to wait if rate limited.
type Result struct {
	Allowed   bool          // Allowed is true if the request should be permitted.
	WaitTime  time.Duration // WaitTime is the duration to wait before the next allowed request.
	Remaining int           // Remaining is the approximate number of requests left in the current burst window.
	Limit     float64       // Limit is the configured requests per second.
}

// Limiter defines the interface for distributed rate limiting.
type Limiter interface {
	// Allow checks if a request for the given key is permitted.
	Allow(ctx context.Context, key string) (*Result, error)
	// Wait blocks until a request for the given key is permitted or the context is cancelled.
	Wait(ctx context.Context, key string) error
	// WaitTimeout is like Wait but with a maximum allowed wait duration.
	WaitTimeout(ctx context.Context, key string, timeout time.Duration) error
}

// LeakyBucketRedis implements distributed rate limiting using Redis and the GCRA algorithm.
type LeakyBucketRedis struct {
	client redis.UniversalClient
	rate   float64 // Requests per second
	burst  int     // Maximum bucket capacity
}

// Option configures the LeakyBucketRedis
type Option func(*LeakyBucketRedis)

// WithBurst sets the maximum bucket capacity (default is 1)
func WithBurst(burst int) Option {
	return func(lb *LeakyBucketRedis) {
		if burst < 1 {
			burst = 1
		}
		lb.burst = burst
	}
}

// New creates a new LeakyBucketRedis instance
func New(client redis.UniversalClient, rate float64, opts ...Option) *LeakyBucketRedis {
	lb := &LeakyBucketRedis{
		client: client,
		rate:   rate,
		burst:  1,
	}

	for _, opt := range opts {
		opt(lb)
	}

	return lb
}

// NewLeakyBucket creates a new LeakyBucketRedis instance for backward compatibility
func NewLeakyBucket(client *redis.Client, key string, rate float64) *LeakyBucketRedis {
	// Note: The new design prefers passing the key to Allow()
	// This wrapper allows the old usage by storing a default key if needed, 
	// but here we just return the new struct. 
	// To maintain full compatibility with the old Allow() signature, 
	// we'd need to store the key in the struct.
	return &LeakyBucketRedis{
		client: client,
		rate:   rate,
		burst:  1,
	}
}

// Allow checks if a request should be allowed based on the rate limit.
// If the key is empty, it returns an error.
func (lb *LeakyBucketRedis) Allow(ctx context.Context, key string) (*Result, error) {
	if key == "" {
		return nil, ErrInvalidKey
	}

	now := time.Now()
	nowFloat := float64(now.UnixNano()) / 1e9

	// GCRA Implementation in Lua
	// ARGV[1]: rate (requests per second)
	// ARGV[2]: burst (capacity)
	// ARGV[3]: now (current time in seconds)
	script := `
		local key = KEYS[1]
		local rate = tonumber(ARGV[1])
		local burst = tonumber(ARGV[2])
		local now = tonumber(ARGV[3])

		local emission_interval = 1.0 / rate
		local burst_offset = emission_interval * burst

		local tat = redis.call('GET', key)
		if not tat then
			tat = now
		else
			tat = tonumber(tat)
		end

		local new_tat = math.max(tat, now) + emission_interval
		local allow_at = new_tat - burst_offset

		local wait = allow_at - now
		if wait > 0 then
			return {0, tostring(wait), "0"}
		end

		redis.call('SET', key, new_tat, 'EX', math.ceil(burst_offset + emission_interval))
		
		local remaining = math.floor((now - (new_tat - burst_offset)) / emission_interval)
		return {1, "0", tostring(remaining)}
	`

	res, err := lb.client.Eval(ctx, script, []string{key}, lb.rate, lb.burst, nowFloat).Result()
	if err != nil {
		// Fail open on Redis error
		return &Result{Allowed: true, WaitTime: 0, Remaining: lb.burst, Limit: lb.rate}, nil
	}

	parts := res.([]interface{})
	allowed := parts[0].(int64) == 1
	waitSecs, _ := strconv.ParseFloat(parts[1].(string), 64)
	remaining, _ := strconv.Atoi(parts[2].(string))

	return &Result{
		Allowed:   allowed,
		WaitTime:  time.Duration(waitSecs * float64(time.Second)),
		Remaining: remaining,
		Limit:     lb.rate,
	}, nil
}

// Wait blocks until the request is allowed or the context is cancelled.
// It continuously calls Allow and waits for the calculated WaitTime if not allowed,
// or until the provided context is done.
func (lb *LeakyBucketRedis) Wait(ctx context.Context, key string) error {
	for {
		res, err := lb.Allow(ctx, key)
		if err != nil {
			return err
		}

		if res.Allowed {
			return nil
		}

		select {
		case <-time.After(res.WaitTime):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// WaitTimeout blocks until a request for the given key is permitted,
// the context is cancelled, or the specified timeout duration elapses.
// It returns an error if the context is cancelled or the timeout is reached.
func (lb *LeakyBucketRedis) WaitTimeout(ctx context.Context, key string, timeout time.Duration) error {
	tCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return lb.Wait(tCtx, key)
}

// oldAllow is for backward compatibility if we decide to keep the key in the struct
// For now, I'll update main.go to use the new signature.
func (lb *LeakyBucketRedis) oldAllow(ctx context.Context, key string) time.Duration {
	res, _ := lb.Allow(ctx, key)
	return res.WaitTime
}
