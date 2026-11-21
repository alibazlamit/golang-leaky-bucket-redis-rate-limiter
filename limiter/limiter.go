// Package limiter is a distributed rate limiter backed by Redis.
//
// It implements GCRA, the meter form of the leaky bucket: one Redis key per
// client, one atomic Lua call per decision, Redis server time as the clock.
package limiter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidRate   = errors.New("limiter: rate must be greater than 0 and at most 1,000,000 per second")
	ErrWindowTooLong = errors.New("limiter: burst divided by rate must be under 100 years")
	ErrInvalidBurst  = errors.New("limiter: burst must be at least 1")
	ErrInvalidCost   = errors.New("limiter: cost must be between 1 and the burst size")
	ErrEmptyKey      = errors.New("limiter: key is empty")
	ErrNilClient     = errors.New("limiter: redis client is nil")
)

const (
	defaultPrefix = "ratelimit"
	maxKeyLen     = 64
	maxWindow     = 100 * 365 * 24 * time.Hour
)

var gcra = redis.NewScript(`
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

local t = redis.call('TIME')
local now = t[1] * 1000000 + t[2]

local window = interval * burst

-- A stored value outside [now, now + window] cannot come from this script. It
-- means the Redis clock moved (failover) or the key was written by something
-- else. Clamping bounds the damage to one window instead of a long lockout.
local tat = tonumber(redis.call('GET', KEYS[1]) or now)
if not tat or tat ~= tat or tat < now then tat = now end
if tat > now + window then tat = now + window end

local new_tat = tat + interval * cost
local allow_at = new_tat - window

if allow_at > now then
	local remaining = math.max(0, math.floor((now + window - tat) / interval))
	return {0, remaining, math.ceil(allow_at - now), math.ceil(tat - now)}
end

redis.call('SET', KEYS[1], string.format('%.0f', new_tat), 'PX', math.ceil((new_tat - now) / 1000))
return {1, math.floor((now - allow_at) / interval), 0, math.ceil(new_tat - now)}
`)

// Result describes one rate limit decision.
type Result struct {
	Allowed bool
	// Limit is the burst size: the most requests a client can make at once.
	Limit int
	// Remaining is how many more requests would be allowed right now.
	Remaining int
	// RetryAfter is how long to wait before the same request would be allowed.
	// Zero when Allowed is true.
	RetryAfter time.Duration
	// ResetAfter is how long until the client is back to a full burst.
	ResetAfter time.Duration
}

// Limiter is safe for concurrent use.
type Limiter struct {
	client   redis.Scripter
	rate     float64
	interval float64
	burst    int
	prefix   string
	failOpen bool
}

type Option func(*Limiter)

// WithBurst sets how many requests may arrive at once. The default is 1,
// which spaces requests evenly at the configured rate.
func WithBurst(burst int) Option {
	return func(l *Limiter) { l.burst = burst }
}

// WithKeyPrefix namespaces the Redis keys. The default is "ratelimit".
//
// Limiters with different rate or burst never share state, because both are
// part of the Redis key. Give two limiters with identical settings different
// prefixes if they must count separately.
func WithKeyPrefix(prefix string) Option {
	return func(l *Limiter) { l.prefix = prefix }
}

// WithFailOpen allows requests when Redis cannot be reached. The default is
// to deny them. Either way Allow returns the error, so an outage is never
// silent.
func WithFailOpen() Option {
	return func(l *Limiter) { l.failOpen = true }
}

// New returns a limiter that permits rate requests per second per key.
func New(client redis.Scripter, rate float64, opts ...Option) (*Limiter, error) {
	if client == nil {
		return nil, ErrNilClient
	}
	if !(rate > 0) || math.IsInf(rate, 0) {
		return nil, ErrInvalidRate
	}
	// A whole number of microseconds keeps every value in the script an exact
	// integer. With a fractional interval, float rounding near the current
	// Unix time costs the client the last request of its burst.
	interval := math.Round(float64(time.Second/time.Microsecond) / rate)
	if interval < 1 {
		return nil, ErrInvalidRate
	}
	l := &Limiter{
		client:   client,
		rate:     rate,
		interval: interval,
		burst:    1,
		prefix:   defaultPrefix,
	}
	for _, opt := range opts {
		opt(l)
	}
	if l.burst < 1 {
		return nil, ErrInvalidBurst
	}
	if l.interval*float64(l.burst) > float64(maxWindow/time.Microsecond) {
		return nil, ErrWindowTooLong
	}
	l.prefix += ":" + strconv.FormatFloat(l.rate, 'g', -1, 64) + "/" + strconv.Itoa(l.burst) + ":"
	return l, nil
}

// Allow is AllowN with a cost of 1.
func (l *Limiter) Allow(ctx context.Context, key string) (Result, error) {
	return l.AllowN(ctx, key, 1)
}

// AllowN reports whether a request that costs n may proceed.
//
// A non-nil error means no decision could be read from Redis. In that case
// Result.Allowed carries the configured policy: false by default, true with
// WithFailOpen. An empty key or a cancelled context is always denied.
func (l *Limiter) AllowN(ctx context.Context, key string, n int) (Result, error) {
	denied := Result{Limit: l.burst}
	if key == "" {
		return denied, ErrEmptyKey
	}
	if n < 1 || n > l.burst {
		return denied, ErrInvalidCost
	}

	raw, err := gcra.Run(ctx, l.client, []string{l.prefix + boundedKey(key)}, l.interval, l.burst, n).Int64Slice()
	if err == nil && len(raw) != 4 {
		err = fmt.Errorf("limiter: unexpected script reply of %d values", len(raw))
	}
	if err != nil {
		if ctx.Err() != nil {
			return denied, ctx.Err()
		}
		return Result{Allowed: l.failOpen, Limit: l.burst}, fmt.Errorf("limiter: redis: %w", err)
	}

	return Result{
		Allowed:    raw[0] == 1,
		Limit:      l.burst,
		Remaining:  int(raw[1]),
		RetryAfter: time.Duration(raw[2]) * time.Microsecond,
		ResetAfter: time.Duration(raw[3]) * time.Microsecond,
	}, nil
}

// Wait blocks until a request for key is allowed or ctx is done.
// With WithFailOpen it returns nil when Redis is unreachable.
func (l *Limiter) Wait(ctx context.Context, key string) error {
	for {
		res, err := l.Allow(ctx, key)
		if res.Allowed {
			return nil
		}
		if err != nil {
			return err
		}

		timer := time.NewTimer(res.RetryAfter)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// boundedKey keeps a client from choosing how much Redis memory one key costs.
// The k: and h: tags stop a short key from being mistaken for a hashed one.
func boundedKey(key string) string {
	if len(key) <= maxKeyLen {
		return "k:" + key
	}
	sum := sha256.Sum256([]byte(key))
	return "h:" + hex.EncodeToString(sum[:16])
}
