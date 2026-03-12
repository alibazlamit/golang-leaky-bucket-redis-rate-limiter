package leaky_bucket_redis

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

// EchoMiddleware returns an Echo-compatible middleware
func EchoMiddleware(limiter Limiter, extractor KeyExtractor, opts ...MiddlewareOption) echo.MiddlewareFunc {
	config := &middlewareConfig{
		errorHandler: func(w http.ResponseWriter, r *http.Request, res *Result) {
			// This default is ignored for Echo as we use Echo's own context below
		},
	}

	for _, opt := range opts {
		opt(config)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := extractor(c.Request())
			res, err := limiter.Allow(c.Request().Context(), key)

			if err != nil {
				return next(c)
			}

			c.Response().Header().Set("X-RateLimit-Limit", strconv.FormatFloat(res.Limit, 'f', -1, 64))
			c.Response().Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))

			if !res.Allowed {
				if config.onLimit != nil {
					config.onLimit(c.Request(), res)
				}

				c.Response().Header().Set("Retry-After", strconv.FormatFloat(res.WaitTime.Seconds(), 'f', 3, 64))
				return c.JSON(http.StatusTooManyRequests, map[string]string{
					"error":       "Rate limit exceeded",
					"retry_after": fmt.Sprintf("%.3fs", res.WaitTime.Seconds()),
				})
			}

			return next(c)
		}
	}
}
