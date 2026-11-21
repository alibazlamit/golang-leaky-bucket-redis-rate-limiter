# Leaky bucket rate limiter for Go and Redis

[![Tests](https://github.com/alibazlamit/golang-leaky-bucket-redis-rate-limiter/actions/workflows/test.yml/badge.svg)](https://github.com/alibazlamit/golang-leaky-bucket-redis-rate-limiter/actions/workflows/test.yml)
[![Coverage](https://img.shields.io/badge/coverage-%E2%89%A595%25%20enforced%20in%20CI-brightgreen)](.github/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/alibazlamit/leaky_bucket_redis/v3/limiter.svg)](https://pkg.go.dev/github.com/alibazlamit/leaky_bucket_redis/v3/limiter)
[![Go Report Card](https://goreportcard.com/badge/github.com/alibazlamit/leaky_bucket_redis/v3)](https://goreportcard.com/report/github.com/alibazlamit/leaky_bucket_redis/v3)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**The Redis rate limiter for Go that is hard to bypass by accident.**

Most rate limiters get the algorithm right and leave the dangerous part to
you: which client is this, and what happens when Redis is down? This one
ships safe answers to both, on top of an atomic GCRA (leaky bucket) script.

## Try it in two minutes

```bash
docker run -d --rm --name redis -p 6379:6379 redis:7
mkdir demo && cd demo && go mod init demo
go get github.com/alibazlamit/leaky_bucket_redis/v3
```

`main.go`:

```go
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
```

```bash
go run . &
for i in 1 2 3 4 5 6 7; do curl -s -o /dev/null -w '%{http_code} ' localhost:8080; done
# 200 200 200 200 200 429 429
```

The sixth response looks like this:

```
HTTP/1.1 429 Too Many Requests
Retry-After: 1
X-Ratelimit-Limit: 5
X-Ratelimit-Remaining: 0
X-Ratelimit-Reset: 5
```

## What "hard to bypass" means

| Attack or failure | What this library does | Test |
|---|---|---|
| Client sends a fake `X-Forwarded-For` on every request | Ignored. The header is only read when the request comes from a proxy range you listed. | `TestClientIPIgnoresForwardedHeaderByDefault` |
| Client rotates through its IPv6 `/64` (18 quintillion addresses) | IPv6 clients are limited per `/64`, not per address. | `TestClientIPGroupsIPv6By64` |
| Request cannot be tied to a client (the key is empty) | Rejected with `400`. It is never waved through unlimited. | `TestMiddlewareRejectsRequestsWithoutAKey` |
| Redis is down | Denied with `503` by default. `WithFailOpen()` allows instead. Both report the error. | `TestMiddlewareWhenRedisIsDown` |
| Rate is `0` or negative | `New` returns `ErrInvalidRate` instead of building a limiter that allows everything. | `TestNewRejectsBadConfig` |
| App servers have different clocks | Irrelevant. The script uses Redis server time. | `TestUsesRedisClockNotLocalClock` |
| Two limiters reuse the same key with different settings | They never share state. Rate and burst are part of the Redis key. | `TestLimitersWithDifferentSettingsDoNotShareState` |
| Client sends a 100 KB key to fill Redis memory | Keys over 64 bytes are stored as a hash. | `TestLongKeysAreHashed` |
| Redis clock jumps after a failover, or a key holds garbage | The stored value is clamped to one window, so the worst case is a short wait, not a day-long lockout or a free pass. | `TestBadStoredStateHealsWithinOneWindow` |
| 200 requests arrive at the same instant | Exactly the burst is allowed. Check and update are one atomic script. | `TestConcurrentCallersGetExactlyTheBurst` |

Every row is a test you can run: `go test ./limiter -run <name> -v`.

## Install

```bash
go get github.com/alibazlamit/leaky_bucket_redis/v3
```

Requires Go 1.25+ and Redis 5+ (or a compatible server). Works with
`*redis.Client`, `*redis.ClusterClient` and `*redis.Ring`.

The core package depends only on `go-redis`. Gin and Echo are compiled into
your binary only if you import their adapter package.

## Usage

### Direct

```go
l, err := limiter.New(client, 10, limiter.WithBurst(20)) // 10 per second, bursts of 20
if err != nil {
	log.Fatal(err)
}

res, err := l.Allow(ctx, "user:42")
if err != nil {
	log.Printf("rate limiter: %v", err) // Redis problem. res.Allowed holds your fail policy.
}
if !res.Allowed {
	fmt.Printf("slow down, retry in %v\n", res.RetryAfter)
	return
}
fmt.Printf("%d of %d left\n", res.Remaining, res.Limit)
```

### net/http, Gin, Echo

```go
http.ListenAndServe(":8080", limiter.Middleware(l, limiter.ClientIP())(mux))

r.Use(ginlimiter.Middleware(l, limiter.ClientIP()))   // .../v3/ginlimiter

e.Use(echolimiter.Middleware(l, limiter.ClientIP()))  // .../v3/echolimiter
```

All three set `X-RateLimit-Limit`, `X-RateLimit-Remaining` and
`X-RateLimit-Reset`, and add `Retry-After` in whole seconds when they limit.
All three accept `WithDenyHandler` to replace the response and `WithOnError`
to log Redis errors.

### Batch endpoints

If one request can carry 500 items, a per-request limit is not a limit.
Charge per item:

```go
res, err := l.AllowN(ctx, "user:42", len(items))
```

### Background workers

```go
for _, job := range jobs {
	if err := l.Wait(ctx, "email-provider"); err != nil {
		return err
	}
	send(job)
}
```

## Choosing the key

The key decides who shares a bucket. Use something the client cannot choose.

| Key | Use it when |
|---|---|
| `ClientIP()` | Clients connect to your server directly. |
| `ClientIP(prefixes...)` | You sit behind a load balancer or CDN. Pass its address ranges. |
| Your own `func(*http.Request) string` | You have an identity that your auth already verified, such as a user or tenant ID. |
| `FirstOf(a, b)` | Signed-in users get their own bucket, everyone else is limited by IP. |

Behind a proxy:

```go
key := limiter.ClientIP(netip.MustParsePrefix("10.0.0.0/8"))
```

By verified user, with IP for anonymous traffic:

```go
verifiedUser := func(r *http.Request) string {
	id, _ := r.Context().Value(userIDKey).(string) // set by your auth middleware
	return id
}
key := limiter.FirstOf(verifiedUser, limiter.ClientIP())
```

### Why there is no `Header("X-API-Key")` helper

v2 had one. It is a bypass. A client that sends a new random key on every
request gets a new bucket on every request, so it is never limited. The same
is true for cookies. Run the limiter after authentication and key by the
identity it resolved, or key by IP.

## When Redis is down

You have to pick one, and the right answer depends on what you protect.

| | Default (fail closed) | `WithFailOpen()` |
|---|---|---|
| Requests during an outage | Denied with `503` | Allowed |
| Good for | Login, signup, payments, anything expensive | Read traffic where availability beats protection |
| Error visible to you | Yes, from `Allow` and `WithOnError` | Yes, from `Allow` and `WithOnError` |

How long a request waits on a slow Redis is set by the `ReadTimeout` of your
`go-redis` client and by the request context.

## How it works

GCRA stores one number per client: the time at which their bucket will be
empty again. A request is allowed if adding its cost would not push that time
more than one burst into the future. That gives the same behavior as a leaky
bucket with one Redis key, one round trip, and no background job to refill
anything. Keys expire on their own once the bucket has drained.

The check and the update run inside one Lua script, so two app servers can
never both take the last slot. The script is sent once and then called by its
hash.

## How it compares

[`go-redis/redis_rate`](https://github.com/go-redis/redis_rate) is the
well-known GCRA limiter for Go and it is a good library. It gives you the
algorithm. If that is all you need, use it.

This library adds the layer around the algorithm: HTTP middleware for
net/http, Gin and Echo, client identification that cannot be spoofed by
default, and an explicit choice for Redis outages.

### Speed

Both libraries make one Redis round trip per decision, so they cost the same.
This is not a faster limiter. It is a safer one at the same price.

| | Time per `Allow` | Memory | Allocations |
|---|---|---|---|
| This library | 29.6 µs | 576 B | 19 |
| `redis_rate` v10 | 30.0 µs | 688 B | 23 |

Mean of 6 runs, 4 parallel callers, Redis 7 on the same host, GitHub Actions
`ubuntu-latest` (Intel Xeon Platinum 8573C). The 1% gap in time is inside
run-to-run noise. The
[benchmark job](https://github.com/alibazlamit/golang-leaky-bucket-redis-rate-limiter/actions/workflows/test.yml)
reruns on every pull request, so these numbers can be checked. To run it yourself:

```bash
cd benchmarks && REDIS_ADDR=localhost:6379 go test -run '^$' -bench . -benchtime 3s -count 6
```

## Upgrading from v2

v3 is a breaking release. [CHANGELOG.md](CHANGELOG.md) has the full list.

| v2 | v3 |
|---|---|
| `import ".../v2/leaky_bucket"` | `import ".../v3/limiter"` |
| `New(client, rate)` returns `*LeakyBucketRedis` | `New(client, rate)` returns `(*Limiter, error)` |
| `Allow` returns `(*Result, error)`, error always nil on Redis failure | `Allow` returns `(Result, error)`, error is real |
| Fails open, silently | Fails closed. `WithFailOpen()` to opt in. |
| `Result.WaitTime` | `Result.RetryAfter` |
| `Result.Limit` is the rate | `Result.Limit` is the burst size |
| `ExtractIP` (trusted `X-Forwarded-For` from anyone) | `ClientIP()` / `ClientIP(trustedProxies...)` |
| `ExtractHeader`, `ExtractCookie` | Removed. See "Choosing the key". |
| `GinMiddleware`, `EchoMiddleware` | `ginlimiter.Middleware`, `echolimiter.Middleware` |
| `WithErrorHandler`, `WithOnLimit` | `WithDenyHandler`, `WithOnError` |
| `WaitTimeout(ctx, key, d)` | `Wait` with `context.WithTimeout` |
| Redis keys are the raw key | `ratelimit:<rate>/<burst>:k:<key>` (prefix set by `WithKeyPrefix`) |

## Development

```bash
go test -race ./...                              # in-memory Redis, nothing to install
REDIS_ADDR=localhost:6379 go test -race ./...    # also runs the real Redis test
golangci-lint run
```

Runnable programs live in [`examples/`](examples): `nethttp`, `gin`, `echo` and `worker`.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).
Pushing a `v*` tag publishes the GitHub release.

## License

MIT. See [LICENSE](LICENSE).
