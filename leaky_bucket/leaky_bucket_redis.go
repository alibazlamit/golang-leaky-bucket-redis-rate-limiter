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

// LeakyBucketRedis implements distributed rate limiting using Redis
type LeakyBucketRedis struct {
	client *redis.Client
	key    string
	rate   float64 // Requests per second
}

// NewLeakyBucket creates a new LeakyBucketRedis instance
// Returns nil if validation fails
func NewLeakyBucket(client *redis.Client, key string, rate float64) *LeakyBucketRedis {
	if key == "" || rate <= 0 {
		return nil
	}

	lb := &LeakyBucketRedis{
		client: client,
		key:    key,
		rate:   rate,
	}
	return lb
}

// Allow checks if a request should be allowed based on the rate limit.
// Returns 0 if allowed immediately, or wait duration if rate limited.
// Uses high-precision timestamps for accurate rate limiting.
func (lb *LeakyBucketRedis) Allow(ctx context.Context) time.Duration {
	now := time.Now()
	nowFloat := float64(now.UnixNano()) / 1e9 // High precision timestamp

	// Improved Lua script with proper time handling
	script := `
		local key = KEYS[1]
		local ts = tonumber(ARGV[1])   -- current time in seconds (high precision)
		local rate = tonumber(ARGV[2]) -- requests per second

		-- Remove tokens older than 1 second
		local min_time = ts - 1
		redis.call('ZREMRANGEBYSCORE', key, '-inf', min_time)

		-- Get the last (most recent) token
		local last_tokens = redis.call('ZREVRANGE', key, 0, 0, 'WITHSCORES')
		local next_time = ts

		-- If there's a previous token, calculate when the next token can be added
		if #last_tokens > 0 then
			local last_time = tonumber(last_tokens[2])
			local token_interval = 1.0 / rate
			next_time = last_time + token_interval
			
			-- If current time is past the next allowed time, use current time
			if ts >= next_time then
				next_time = ts
			end
		end

		-- Add the new token
		redis.call('ZADD', key, next_time, next_time)
		
		-- Set expiration to prevent memory leaks (2 seconds should be enough)
		redis.call('EXPIRE', key, 2)

		-- Calculate wait time
		local wait = next_time - ts
		return tostring(math.max(0, wait))
	`

	result, err := lb.client.Eval(ctx, script, []string{lb.key}, nowFloat, lb.rate).Result()
	if err != nil {
		// On Redis error, fail open (allow the request) to prevent cascading failures
		return 0
	}

	// Convert result to duration
	waitSeconds, err := strconv.ParseFloat(result.(string), 64)
	if err != nil {
		return 0
	}

	if waitSeconds <= 0 {
		return 0
	}

	return time.Duration(waitSeconds * float64(time.Second))
}
