package leaky_bucket_redis

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// GinMiddleware returns a Gin-compatible middleware
func GinMiddleware(limiter Limiter, extractor KeyExtractor, opts ...MiddlewareOption) gin.HandlerFunc {
	config := &middlewareConfig{
		errorHandler: func(w http.ResponseWriter, r *http.Request, res *Result) {
			w.Header().Set("Retry-After", strconv.FormatFloat(res.WaitTime.Seconds(), 'f', 3, 64))
			// Default placeholder
		},
	}

	for _, opt := range opts {
		opt(config)
	}

	return func(c *gin.Context) {
		key := extractor(c.Request)
		res, err := limiter.Allow(c.Request.Context(), key)

		if err != nil {
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.FormatFloat(res.Limit, 'f', -1, 64))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))

		if !res.Allowed {
			if config.onLimit != nil {
				config.onLimit(c.Request, res)
			}
			
			// If a custom error handler is provided, use it. 
			// Otherwise, use a default Gin response.
			c.Header("Retry-After", strconv.FormatFloat(res.WaitTime.Seconds(), 'f', 3, 64))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Rate limit exceeded",
				"retry_after": fmt.Sprintf("%.3fs", res.WaitTime.Seconds()),
			})
			return
		}

		c.Next()
	}
}
