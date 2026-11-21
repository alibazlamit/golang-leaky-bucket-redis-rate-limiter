package limiter

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return srv, client
}

func frozen(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	srv, client := setup(t)
	srv.SetTime(epoch)
	return srv, client
}

func mustNew(t *testing.T, client redis.Scripter, rate float64, opts ...Option) *Limiter {
	t.Helper()
	l, err := New(client, rate, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNewRejectsBadConfig(t *testing.T) {
	_, client := setup(t)
	for _, rate := range []float64{0, -5, math.NaN(), math.Inf(1), 1e7, 700_000, 650_000, 90_000, 45_000} {
		if _, err := New(client, rate); !errors.Is(err, ErrInvalidRate) {
			t.Errorf("rate %v: got %v, want ErrInvalidRate", rate, err)
		}
	}
	if _, err := New(client, 1, WithBurst(0)); !errors.Is(err, ErrInvalidBurst) {
		t.Errorf("burst 0: got %v, want ErrInvalidBurst", err)
	}
	if _, err := New(client, 1e-12); !errors.Is(err, ErrWindowTooLong) {
		t.Errorf("tiny rate: got %v, want ErrWindowTooLong", err)
	}
	if _, err := New(nil, 1); !errors.Is(err, ErrNilClient) {
		t.Errorf("nil client: got %v, want ErrNilClient", err)
	}
}

func TestBurstThenRefill(t *testing.T) {
	srv, client := frozen(t)
	l := mustNew(t, client, 10, WithBurst(3))
	ctx := context.Background()

	for want := 2; want >= 0; want-- {
		res, err := l.Allow(ctx, "k")
		if err != nil || !res.Allowed || res.Remaining != want || res.Limit != 3 {
			t.Fatalf("want allowed with %d remaining, got %+v err=%v", want, res, err)
		}
	}

	res, err := l.Allow(ctx, "k")
	if err != nil || res.Allowed || res.Remaining != 0 {
		t.Fatalf("4th request: want denied, got %+v err=%v", res, err)
	}
	if res.RetryAfter != 100*time.Millisecond || res.ResetAfter != 300*time.Millisecond {
		t.Fatalf("got retry=%v reset=%v, want 100ms and 300ms", res.RetryAfter, res.ResetAfter)
	}

	srv.SetTime(epoch.Add(99 * time.Millisecond))
	if res, _ := l.Allow(ctx, "k"); res.Allowed || res.RetryAfter != time.Millisecond {
		t.Fatalf("at 99ms: want denied with 1ms left, got %+v", res)
	}

	srv.SetTime(epoch.Add(100 * time.Millisecond))
	if res, _ := l.Allow(ctx, "k"); !res.Allowed {
		t.Fatalf("at 100ms: want allowed, got %+v", res)
	}

	srv.SetTime(epoch.Add(time.Hour))
	if res, _ := l.Allow(ctx, "k"); !res.Allowed || res.Remaining != 2 {
		t.Fatalf("after idle: want a full burst again, got %+v", res)
	}
}

func TestDeniedRequestsDoNotExtendTheWait(t *testing.T) {
	_, client := frozen(t)
	l := mustNew(t, client, 1)
	ctx := context.Background()

	hit(t, l, "k")
	for range 50 {
		if res, _ := l.Allow(ctx, "k"); res.Allowed || res.RetryAfter != time.Second {
			t.Fatalf("want a steady 1s wait, got %+v", res)
		}
	}
}

func TestKeysAreIndependent(t *testing.T) {
	_, client := frozen(t)
	l := mustNew(t, client, 1)
	ctx := context.Background()

	hit(t, l, "a")
	if res, _ := l.Allow(ctx, "b"); !res.Allowed {
		t.Fatal("key b was limited by key a")
	}
}

func TestAllowN(t *testing.T) {
	_, client := frozen(t)
	l := mustNew(t, client, 10, WithBurst(10))
	ctx := context.Background()

	if res, _ := l.AllowN(ctx, "k", 4); !res.Allowed || res.Remaining != 6 {
		t.Fatalf("cost 4: got %+v", res)
	}
	if res, _ := l.AllowN(ctx, "k", 7); res.Allowed || res.Remaining != 6 || res.RetryAfter != 100*time.Millisecond {
		t.Fatalf("cost 7 with 6 left: got %+v", res)
	}
	for _, n := range []int{0, -1, 11} {
		if res, err := l.AllowN(ctx, "k", n); res.Allowed || !errors.Is(err, ErrInvalidCost) {
			t.Fatalf("cost %d: got %+v err=%v", n, res, err)
		}
	}
}

func TestFractionalRate(t *testing.T) {
	srv, client := frozen(t)
	l := mustNew(t, client, 0.5)
	ctx := context.Background()

	hit(t, l, "k")
	if res, _ := l.Allow(ctx, "k"); res.Allowed || res.RetryAfter != 2*time.Second {
		t.Fatalf("got %+v, want a 2s wait", res)
	}
	srv.SetTime(epoch.Add(2 * time.Second))
	if res, _ := l.Allow(ctx, "k"); !res.Allowed {
		t.Fatalf("got %+v, want allowed after 2s", res)
	}
}

func TestConcurrentCallersGetExactlyTheBurst(t *testing.T) {
	_, client := frozen(t)
	l := mustNew(t, client, 1, WithBurst(10))

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			if res, err := l.Allow(context.Background(), "k"); err == nil && res.Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()

	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed %d of 200, want exactly 10", got)
	}
}

func TestEmptyKeyIsDenied(t *testing.T) {
	_, client := setup(t)
	l := mustNew(t, client, 1, WithFailOpen())

	if res, err := l.Allow(context.Background(), ""); res.Allowed || !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("got %+v err=%v", res, err)
	}
}

func TestRedisDown(t *testing.T) {
	srv, client := setup(t)
	closed := mustNew(t, client, 1)
	open := mustNew(t, client, 1, WithFailOpen())
	srv.Close()

	if res, err := closed.Allow(context.Background(), "k"); res.Allowed || err == nil {
		t.Fatalf("default: want denied with an error, got %+v err=%v", res, err)
	}
	if res, err := open.Allow(context.Background(), "k"); !res.Allowed || err == nil {
		t.Fatalf("fail open: want allowed with an error, got %+v err=%v", res, err)
	}
	if err := closed.Wait(context.Background(), "k"); err == nil {
		t.Fatal("Wait: want an error when Redis is down")
	}
	if err := open.Wait(context.Background(), "k"); err != nil {
		t.Fatalf("Wait with fail open: got %v", err)
	}
}

func TestCancelledContextIsDeniedEvenWithFailOpen(t *testing.T) {
	_, client := setup(t)
	l := mustNew(t, client, 1, WithFailOpen())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if res, err := l.Allow(ctx, "k"); res.Allowed || !errors.Is(err, context.Canceled) {
		t.Fatalf("Allow: got %+v err=%v", res, err)
	}
	if err := l.Wait(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait: got %v", err)
	}
}

func TestWait(t *testing.T) {
	_, client := setup(t)
	l := mustNew(t, client, 20)

	start := time.Now()
	for range 3 {
		if err := l.Wait(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("3 requests at 20/s took %v, want about 100ms", elapsed)
	}

	slow := mustNew(t, client, 0.1)
	hit(t, slow, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := slow.Wait(ctx, "slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}

func TestKeyIsPrefixedAndExpires(t *testing.T) {
	srv, client := frozen(t)

	hit(t, mustNew(t, client, 2, WithBurst(4)), "k")
	if ttl := srv.TTL("ratelimit:2/4:k:k"); ttl != 500*time.Millisecond {
		t.Fatalf("ttl = %v, want 500ms", ttl)
	}

	hit(t, mustNew(t, client, 2, WithKeyPrefix("api")), "k")
	if !srv.Exists("api:2/1:k:k") {
		t.Fatal("custom prefix was not used")
	}
}

func TestRealRedis(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR to run against a real Redis")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	key := "test:" + time.Now().Format(time.RFC3339Nano)
	l := mustNew(t, client, 7, WithBurst(5))

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if res, err := l.Allow(ctx, key); err != nil {
				t.Error(err)
			} else if res.Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got < 5 || got > 6 {
		t.Fatalf("allowed %d of 100, want 5 (6 if the burst straddled a refill)", got)
	}

	res, err := l.Allow(ctx, key)
	if err != nil || res.Allowed || res.RetryAfter <= 0 || res.RetryAfter > 143*time.Millisecond {
		t.Fatalf("got %+v err=%v", res, err)
	}
	time.Sleep(res.RetryAfter)
	if res, _ := l.Allow(ctx, key); !res.Allowed {
		t.Fatalf("after waiting RetryAfter: got %+v", res)
	}
}

func TestUsesRedisClockNotLocalClock(t *testing.T) {
	srv, client := setup(t)
	longAgo := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	srv.SetTime(longAgo)
	l := mustNew(t, client, 1)
	ctx := context.Background()

	hit(t, l, "k")
	if res, _ := l.Allow(ctx, "k"); res.Allowed || res.RetryAfter != time.Second {
		t.Fatalf("got %+v, want a 1s wait measured on the Redis clock", res)
	}
	srv.SetTime(longAgo.Add(time.Second))
	if res, _ := l.Allow(ctx, "k"); !res.Allowed {
		t.Fatalf("got %+v, want allowed once the Redis clock moved 1s", res)
	}
}

func TestLimitersWithDifferentSettingsDoNotShareState(t *testing.T) {
	_, client := frozen(t)
	strict := mustNew(t, client, 1)
	loose := mustNew(t, client, 1, WithBurst(100))
	ctx := context.Background()

	for range 50 {
		hit(t, loose, "k")
	}
	if res, _ := strict.Allow(ctx, "k"); !res.Allowed {
		t.Fatalf("strict limiter was affected by the loose one: %+v", res)
	}
	if res, _ := strict.Allow(ctx, "k"); res.Allowed {
		t.Fatal("strict limiter did not limit")
	}
	if res, _ := loose.Allow(ctx, "k"); !res.Allowed || res.Remaining != 49 {
		t.Fatalf("loose limiter was affected by the strict one: %+v", res)
	}
}

func TestLongKeysAreHashed(t *testing.T) {
	srv, client := frozen(t)
	l := mustNew(t, client, 1)
	ctx := context.Background()
	long := strings.Repeat("a", 100_000)

	hit(t, l, long)
	if res, _ := l.Allow(ctx, long); res.Allowed {
		t.Fatal("a long key was not limited")
	}
	if res, _ := l.Allow(ctx, long+"b"); !res.Allowed {
		t.Fatal("two different long keys shared a bucket")
	}
	for _, key := range srv.Keys() {
		if len(key) > 64 {
			t.Fatalf("Redis key is %d bytes", len(key))
		}
	}

	victim := strings.Repeat("v", 100)
	hit(t, l, victim)
	for _, key := range srv.Keys() {
		forged := strings.TrimPrefix(key, "ratelimit:1/1:")
		for _, guess := range []string{forged, strings.TrimPrefix(forged, "h:")} {
			if res, _ := l.Allow(ctx, guess); !res.Allowed {
				t.Fatalf("short key %q landed in a hashed key's bucket", guess)
			}
		}
	}
}

func TestPrefixCannotBlurIntoTheRate(t *testing.T) {
	_, client := frozen(t)
	a := mustNew(t, client, 10.5, WithKeyPrefix("api"))
	b := mustNew(t, client, 0.5, WithKeyPrefix("api1"))

	hit(t, b, "k")
	if res, _ := a.Allow(context.Background(), "k"); !res.Allowed {
		t.Fatalf("limiters with different prefixes shared a bucket: %+v", res)
	}
}

func hit(t *testing.T, l *Limiter, key string) {
	t.Helper()
	if _, err := l.Allow(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestBadStoredStateHealsWithinOneWindow(t *testing.T) {
	srv, client := frozen(t)
	l := mustNew(t, client, 1, WithBurst(5))
	ctx := context.Background()
	const redisKey = "ratelimit:1/5:k:k"

	for _, garbage := range []string{"not a number", "nan"} {
		if err := srv.Set(redisKey, garbage); err != nil {
			t.Fatal(err)
		}
		if res, err := l.Allow(ctx, "k"); err != nil || !res.Allowed || res.Remaining != 4 {
			t.Fatalf("stored %q: got %+v err=%v, want a fresh bucket", garbage, res, err)
		}
	}

	farFuture := strconv.FormatInt(epoch.Add(24*time.Hour).UnixMicro(), 10)
	if err := srv.Set(redisKey, farFuture); err != nil {
		t.Fatal(err)
	}
	res, err := l.Allow(ctx, "k")
	if err != nil || res.Allowed || res.RetryAfter != time.Second {
		t.Fatalf("clock moved back a day: got %+v err=%v, want a 1s wait, not a day", res, err)
	}
}

func TestBurstIsExactAtEveryRate(t *testing.T) {
	_, client := frozen(t)
	ctx := context.Background()

	for _, rate := range []float64{0.3, 0.7, 1.0 / 3, 1, 6, 7, 10, 13, 999, 20_000, 500_000, 1e6} {
		for _, burst := range []int{1, 3, 5, 100} {
			l := mustNew(t, client, rate, WithBurst(burst))
			for i := range burst {
				res, err := l.Allow(ctx, "k")
				if err != nil || !res.Allowed || res.Remaining != burst-1-i {
					t.Fatalf("rate %v burst %d request %d: got %+v err=%v", rate, burst, i+1, res, err)
				}
			}
			if res, _ := l.Allow(ctx, "k"); res.Allowed || res.RetryAfter <= 0 {
				t.Fatalf("rate %v burst %d: request %d was not denied: %+v", rate, burst, burst+1, res)
			}
		}
	}
}

func TestAppliedRateMatchesConfiguredRate(t *testing.T) {
	srv, client := frozen(t)
	ctx := context.Background()

	for _, rate := range []float64{0.7, 7, 333, 19_999, 20_000, 500_000} {
		l := mustNew(t, client, rate)
		now := epoch
		srv.SetTime(now)

		const requests = 300
		for range requests {
			res, err := l.Allow(ctx, "k")
			if err != nil {
				t.Fatal(err)
			}
			if res.Allowed {
				continue
			}
			now = now.Add(res.RetryAfter)
			srv.SetTime(now)
			if res, _ := l.Allow(ctx, "k"); !res.Allowed {
				t.Fatalf("rate %v: still denied after waiting RetryAfter: %+v", rate, res)
			}
		}

		got := now.Sub(epoch).Seconds()
		want := (requests - 1) / rate
		if math.Abs(got-want) > want/100 {
			t.Errorf("rate %v: %d requests took %.6fs, want %.6fs", rate, requests, got, want)
		}
	}
	srv.SetTime(epoch)
}

func TestEveryAcceptedRateIsAppliedWithinOnePercent(t *testing.T) {
	_, client := setup(t)

	for rate := 0.5; rate <= 1e6; rate *= 1.0007 {
		l, err := New(client, rate)
		if err != nil {
			if rate <= 20_000 {
				t.Fatalf("rate %v was rejected: %v", rate, err)
			}
			continue
		}
		applied := 1e6 / l.interval
		if math.Abs(applied-rate) > rate/100 {
			t.Fatalf("rate %v is applied as %v", rate, applied)
		}
	}
}
