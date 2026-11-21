package limiter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
)

type userKey struct{}

var (
	ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	verifiedUser KeyFunc = func(r *http.Request) string {
		user, _ := r.Context().Value(userKey{}).(string)
		return user
	}
)

func get(h http.Handler, edit func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if edit != nil {
		edit(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func getAs(h http.Handler, user string) *httptest.ResponseRecorder {
	return get(h, func(r *http.Request) {
		*r = *r.WithContext(context.WithValue(r.Context(), userKey{}, user))
	})
}

func TestClientIPIgnoresForwardedHeaderByDefault(t *testing.T) {
	_, client := frozen(t)
	h := Middleware(mustNew(t, client, 1), ClientIP())(ok)

	passed := 0
	for i := range 100 {
		rec := get(h, func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "10.9.8."+strconv.Itoa(i))
			r.Header.Set("X-Real-IP", "10.9.7."+strconv.Itoa(i))
		})
		if rec.Code == http.StatusOK {
			passed++
		}
	}
	if passed != 1 {
		t.Fatalf("%d of 100 spoofed requests passed a 1 rps limit, want 1", passed)
	}
}

func TestClientIPGroupsIPv6By64(t *testing.T) {
	_, client := frozen(t)
	h := Middleware(mustNew(t, client, 1), ClientIP())(ok)

	passed := 0
	for i := range 100 {
		rec := get(h, func(r *http.Request) {
			r.RemoteAddr = "[2001:db8:1:2::" + strconv.FormatInt(int64(i+1), 16) + "]:4321"
		})
		if rec.Code == http.StatusOK {
			passed++
		}
	}
	if passed != 1 {
		t.Fatalf("%d of 100 addresses from one /64 passed a 1 rps limit, want 1", passed)
	}
}

func TestClientIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}

	tests := []struct {
		name    string
		remote  string
		xff     []string
		trusted []netip.Prefix
		want    string
	}{
		{"direct ipv4", "203.0.113.7:4321", nil, nil, "203.0.113.7"},
		{"direct ipv6 is grouped by /64", "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:4321", nil, nil, "2001:db8:1:2::/64"},
		{"ipv4 mapped ipv6", "[::ffff:203.0.113.7]:4321", nil, nil, "203.0.113.7"},
		{"untrusted peer cannot set the header", "203.0.113.7:1", []string{"1.1.1.1"}, proxies, "203.0.113.7"},
		{"trusted proxy", "10.0.0.1:1", []string{"198.51.100.4"}, proxies, "198.51.100.4"},
		{"client supplied entries are skipped", "10.0.0.1:1", []string{"1.1.1.1, 198.51.100.4"}, proxies, "198.51.100.4"},
		{"proxy chain", "10.0.0.1:1", []string{"198.51.100.4, 10.0.0.2"}, proxies, "198.51.100.4"},
		{"repeated header lines", "10.0.0.1:1", []string{"1.1.1.1", "198.51.100.4"}, proxies, "198.51.100.4"},
		{"garbage in the header", "10.0.0.1:1", []string{"198.51.100.4, nonsense"}, proxies, "10.0.0.1"},
		{"only proxies in the chain", "10.0.0.1:1", []string{"10.0.0.3, 10.0.0.2"}, proxies, "10.0.0.3"},
		{"trusted proxy without the header", "10.0.0.1:1", nil, proxies, "10.0.0.1"},
		{"unparseable peer", "not-an-address", nil, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(tt.trusted...)(r); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstOf(t *testing.T) {
	key := FirstOf(verifiedUser, ClientIP())

	anonymous := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	anonymous.RemoteAddr = "203.0.113.7:1"
	if got := key(anonymous); got != "203.0.113.7" {
		t.Fatalf("anonymous: got %q", got)
	}

	signedIn := anonymous.WithContext(context.WithValue(context.Background(), userKey{}, "user:42"))
	if got := key(signedIn); got != "user:42" {
		t.Fatalf("signed in: got %q", got)
	}
}

func TestMiddleware(t *testing.T) {
	_, client := frozen(t)
	h := Middleware(mustNew(t, client, 0.4, WithBurst(2)), verifiedUser)(ok)

	rec := getAs(h, "user:42")
	if rec.Code != http.StatusOK || rec.Header().Get("X-RateLimit-Limit") != "2" || rec.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("first: %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatal("Retry-After set on an allowed request")
	}
	getAs(h, "user:42")

	rec = getAs(h, "user:42")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After = %q, want whole seconds rounded up from 2.5s", got)
	}
	if got := rec.Header().Get("X-RateLimit-Reset"); got != "5" {
		t.Fatalf("X-RateLimit-Reset = %q, want 5", got)
	}
}

func TestMiddlewareRejectsRequestsWithoutAKey(t *testing.T) {
	_, client := frozen(t)
	h := Middleware(mustNew(t, client, 1, WithFailOpen()), verifiedUser)(ok)

	for range 100 {
		if rec := get(h, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", rec.Code)
		}
	}
}

func TestMiddlewareWhenRedisIsDown(t *testing.T) {
	srv, client := setup(t)
	closed := mustNew(t, client, 1)
	open := mustNew(t, client, 1, WithFailOpen())
	srv.Close()

	var reported int
	onError := WithOnError(func(*http.Request, error) { reported++ })

	if rec := get(Middleware(closed, ClientIP(), onError)(ok), nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("default: got %d, want 503", rec.Code)
	}
	if rec := get(Middleware(open, ClientIP(), onError)(ok), nil); rec.Code != http.StatusOK {
		t.Fatalf("fail open: got %d, want 200", rec.Code)
	}
	if reported != 2 {
		t.Fatalf("OnError ran %d times, want 2", reported)
	}
}

func TestMiddlewareDenyHandler(t *testing.T) {
	_, client := frozen(t)
	h := Middleware(mustNew(t, client, 1), ClientIP(),
		WithDenyHandler(func(w http.ResponseWriter, _ *http.Request, status int, _ Result) {
			w.Header().Set("X-Status", strconv.Itoa(status))
			w.WriteHeader(http.StatusTeapot)
		}),
	)(ok)

	get(h, nil)
	rec := get(h, nil)
	if rec.Code != http.StatusTeapot || rec.Body.String() != "" || rec.Header().Get("X-Status") != "429" {
		t.Fatalf("got %d %v", rec.Code, rec.Header())
	}
}
