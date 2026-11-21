// Package ginlimiter adapts limiter to Gin.
package ginlimiter

import (
	"net/http"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/gin-gonic/gin"
)

type config struct {
	deny    func(c *gin.Context, status int, res limiter.Result)
	onError func(c *gin.Context, err error)
}

type Option func(*config)

// WithDenyHandler replaces the default JSON response.
func WithDenyHandler(h func(c *gin.Context, status int, res limiter.Result)) Option {
	return func(c *config) { c.deny = h }
}

func WithOnError(h func(c *gin.Context, err error)) Option {
	return func(c *config) { c.onError = h }
}

func Middleware(l limiter.Allower, key limiter.KeyFunc, opts ...Option) gin.HandlerFunc {
	cfg := config{
		deny: func(c *gin.Context, status int, _ limiter.Result) {
			c.AbortWithStatusJSON(status, gin.H{"error": http.StatusText(status)})
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(c *gin.Context) {
		res, err := l.Allow(c.Request.Context(), key(c.Request))
		if err != nil && cfg.onError != nil {
			cfg.onError(c, err)
		}
		if err == nil {
			res.SetHeaders(c.Writer.Header())
		}
		if status := limiter.Status(res, err); status != 0 {
			cfg.deny(c, status, res)
			c.Abort()
			return
		}
		c.Next()
	}
}
