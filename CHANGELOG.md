# Changelog

## v3.0.0

A rewrite around one goal: a rate limiter that is hard to bypass by accident.
v2 could be bypassed in five ways with no error or log line. Each one is now
closed and has a test.

### Fixed

| Problem in v2 | v3 |
|---|---|
| A request with no key (missing header or cookie) skipped the limiter | Rejected with `400` |
| `ExtractIP` trusted `X-Forwarded-For` from any client, so a new fake value per request meant no limit | `ClientIP()` ignores the header unless the request comes from a proxy range you list |
| `rate <= 0` produced a limiter that allowed everything | `New` returns `ErrInvalidRate` |
| `Wait` returned `nil` when its context was already cancelled | Returns the context error |
| Any Redis error was swallowed and the request allowed | `Allow` returns the error. Denied by default, allowed with `WithFailOpen()` |
| `Retry-After` was sent as a fraction such as `0.100` | Whole seconds, rounded up |
| The app server clock decided the limit, so servers with different clocks disagreed | Redis server time |
| Two limiters with different settings and the same key corrupted each other | Rate and burst are part of the Redis key |

### Changed (breaking)

- Import path is `github.com/alibazlamit/leaky_bucket_redis/v3/limiter`.
- Gin and Echo adapters moved to `ginlimiter` and `echolimiter`. The core
  package now depends only on `go-redis`.
- `New` returns `(*Limiter, error)`. `Allow` returns `(Result, error)`.
- `Result.WaitTime` is now `Result.RetryAfter`. `Result.Limit` is the burst
  size. `Result.ResetAfter` is new.
- `ExtractHeader` and `ExtractCookie` are gone and have no replacement. Keying
  by a value the client chooses is a bypass: a new value per request gets a
  new bucket per request. Key by client IP or by an identity your auth
  verified.
- `WithErrorHandler` and `WithOnLimit` became `WithDenyHandler` and
  `WithOnError`, and now work in the Gin and Echo adapters too.
- `WaitTimeout`, `NewLeakyBucket` and the `Limiter` interface are removed.
- Redis keys look like `ratelimit:<rate>/<burst>:k:<key>`. Counters from v2 are
  not carried over.

### Added

- `AllowN` for requests that cost more than one unit.
- `ClientIP(trustedProxies...)`, with IPv6 clients limited per `/64`.
- `FirstOf` to fall back from a verified user ID to the client IP.
- `X-RateLimit-Reset` header.
- Keys longer than 64 bytes are hashed, so a client cannot choose how much
  Redis memory a key costs.
- Stored state outside the valid range is clamped, so a Redis clock jump after
  a failover cannot lock clients out for longer than one window.
- A benchmark against `go-redis/redis_rate` in `benchmarks/`.

See the README for the upgrade table.
