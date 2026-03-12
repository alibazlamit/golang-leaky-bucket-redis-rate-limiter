package leaky_bucket_redis

import (
	"net"
	"net/http"
	"strconv"
)

// KeyExtractor defines a function to extract a rate limiting key from a request
type KeyExtractor func(r *http.Request) string

// ExtractIP returns the client's IP address as the key.
// It handles X-Forwarded-For and X-Real-IP headers.
func ExtractIP(r *http.Request) string {
	// Check for X-Forwarded-For
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	// Check for X-Real-IP
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}
	// Fallback to RemoteAddr
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		return r.RemoteAddr
	}
	return ip
}

// MiddlewareOption configures the standard HTTP middleware
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	errorHandler func(w http.ResponseWriter, r *http.Request, res *Result)
	onLimit      func(r *http.Request, res *Result)
}

// WithErrorHandler sets a custom function to handle rate-limited requests
func WithErrorHandler(h func(w http.ResponseWriter, r *http.Request, res *Result)) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.errorHandler = h
	}
}

// WithOnLimit sets a callback that is triggered whenever a request is rate limited
func WithOnLimit(cb func(r *http.Request, res *Result)) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.onLimit = cb
	}
}

// ExtractHeader returns a KeyExtractor that gets the key from a specific header
func ExtractHeader(name string) KeyExtractor {
	return func(r *http.Request) string {
		return r.Header.Get(name)
	}
}

// ExtractCookie returns a KeyExtractor that gets the key from a specific cookie
func ExtractCookie(name string) KeyExtractor {
	return func(r *http.Request) string {
		cookie, err := r.Cookie(name)
		if err != nil {
			return ""
		}
		return cookie.Value
	}
}

// Middleware returns a standard http.Handler middleware
func Middleware(limiter Limiter, extractor KeyExtractor, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	config := &middlewareConfig{
		errorHandler: func(w http.ResponseWriter, r *http.Request, res *Result) {
			w.Header().Set("Retry-After", strconv.FormatFloat(res.WaitTime.Seconds(), 'f', 3, 64))
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		},
	}

	for _, opt := range opts {
		opt(config)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractor(r)
			res, err := limiter.Allow(r.Context(), key)
			
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.FormatFloat(res.Limit, 'f', -1, 64))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			
			if !res.Allowed {
				if config.onLimit != nil {
					config.onLimit(r, res)
				}
				config.errorHandler(w, r, res)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
