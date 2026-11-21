package limiter

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
)

// Allower is the part of Limiter the middleware needs.
type Allower interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// Status maps a decision to the HTTP status to reply with. Zero means the
// request may proceed.
func Status(res Result, err error) int {
	switch {
	case errors.Is(err, ErrEmptyKey):
		return http.StatusBadRequest
	case res.Allowed:
		return 0
	case err != nil:
		return http.StatusServiceUnavailable
	default:
		return http.StatusTooManyRequests
	}
}

// SetHeaders writes the X-RateLimit-* headers, plus Retry-After when the
// request was limited. Retry-After is rounded up because HTTP only allows
// whole seconds there.
func (r Result) SetHeaders(h http.Header) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(r.Limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(r.Remaining))
	h.Set("X-RateLimit-Reset", strconv.Itoa(int(math.Ceil(r.ResetAfter.Seconds()))))
	if !r.Allowed && r.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int(math.Ceil(r.RetryAfter.Seconds()))))
	}
}

type middlewareConfig struct {
	deny    func(w http.ResponseWriter, r *http.Request, status int, res Result)
	onError func(r *http.Request, err error)
}

type MiddlewareOption func(*middlewareConfig)

// WithDenyHandler replaces the response sent when a request is not let
// through. status is 429, 400 (no key) or 503 (Redis down, fail closed).
func WithDenyHandler(h func(w http.ResponseWriter, r *http.Request, status int, res Result)) MiddlewareOption {
	return func(c *middlewareConfig) { c.deny = h }
}

// WithOnError is called whenever the limiter returns an error, including
// when the request is still let through by WithFailOpen. Use it to log.
func WithOnError(h func(r *http.Request, err error)) MiddlewareOption {
	return func(c *middlewareConfig) { c.onError = h }
}

// Middleware rate limits a net/http handler.
func Middleware(l Allower, key KeyFunc, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := middlewareConfig{
		deny: func(w http.ResponseWriter, _ *http.Request, status int, _ Result) {
			http.Error(w, http.StatusText(status), status)
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res, err := l.Allow(r.Context(), key(r))
			if err != nil && cfg.onError != nil {
				cfg.onError(r, err)
			}
			if err == nil {
				res.SetHeaders(w.Header())
			}
			if status := Status(res, err); status != 0 {
				cfg.deny(w, r, status, res)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
