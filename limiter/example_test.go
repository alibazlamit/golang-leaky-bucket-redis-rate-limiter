package limiter_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisClient() *redis.Client {
	srv, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	return redis.NewClient(&redis.Options{Addr: srv.Addr()})
}

func ExampleLimiter_Allow() {
	client := newRedisClient()

	// 1 request per second, with bursts of 2.
	l, err := limiter.New(client, 1, limiter.WithBurst(2))
	if err != nil {
		panic(err)
	}

	for range 3 {
		res, err := l.Allow(context.Background(), "user:42")
		if err != nil {
			panic(err)
		}
		fmt.Println(res.Allowed, res.Remaining)
	}
	// Output:
	// true 1
	// true 0
	// false 0
}

func ExampleLimiter_AllowN() {
	client := newRedisClient()

	l, err := limiter.New(client, 10, limiter.WithBurst(100))
	if err != nil {
		panic(err)
	}

	// A batch endpoint charges one unit per item, so 60 items in one request
	// cost the same as 60 requests.
	for range 2 {
		res, err := l.AllowN(context.Background(), "user:42", 60)
		if err != nil {
			panic(err)
		}
		fmt.Println(res.Allowed, res.Remaining)
	}
	// Output:
	// true 40
	// false 40
}

func ExampleMiddleware() {
	client := newRedisClient()

	l, err := limiter.New(client, 1, limiter.WithBurst(2))
	if err != nil {
		panic(err)
	}

	hello := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello\n"))
	})
	srv := httptest.NewServer(limiter.Middleware(l, limiter.ClientIP())(hello))
	defer srv.Close()

	for range 3 {
		resp, err := http.Get(srv.URL)
		if err != nil {
			panic(err)
		}
		resp.Body.Close()
		fmt.Println(resp.Status)
	}
	// Output:
	// 200 OK
	// 200 OK
	// 429 Too Many Requests
}

func ExampleClientIP() {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:52100"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.4")

	direct := limiter.ClientIP()
	behindProxy := limiter.ClientIP(netip.MustParsePrefix("10.0.0.0/8"))

	fmt.Println(direct(r))
	fmt.Println(behindProxy(r))
	// Output:
	// ip:10.0.0.1
	// ip:198.51.100.4
}

func ExampleFirstOf() {
	type userID struct{}

	// Your auth middleware verified the user and stored the ID in the context.
	verifiedUser := func(r *http.Request) string {
		id, _ := r.Context().Value(userID{}).(string)
		return id
	}
	key := limiter.FirstOf(verifiedUser, limiter.ClientIP())

	anonymous := httptest.NewRequest(http.MethodGet, "/", nil)
	anonymous.RemoteAddr = "203.0.113.7:52100"
	signedIn := anonymous.WithContext(context.WithValue(anonymous.Context(), userID{}, "user:42"))

	fmt.Println(key(anonymous))
	fmt.Println(key(signedIn))
	// Output:
	// ip:203.0.113.7
	// user:42
}
