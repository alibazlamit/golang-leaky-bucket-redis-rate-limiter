// Package echolimiter adapts limiter to Echo.
package echolimiter

import (
	"net/http"

	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/labstack/echo/v4"
)

type config struct {
	deny    func(c echo.Context, status int, res limiter.Result) error
	onError func(c echo.Context, err error)
}

type Option func(*config)

func WithDenyHandler(h func(c echo.Context, status int, res limiter.Result) error) Option {
	return func(c *config) { c.deny = h }
}

func WithOnError(h func(c echo.Context, err error)) Option {
	return func(c *config) { c.onError = h }
}

func Middleware(l limiter.Allower, key limiter.KeyFunc, opts ...Option) echo.MiddlewareFunc {
	cfg := config{
		deny: func(c echo.Context, status int, _ limiter.Result) error {
			return c.JSON(status, map[string]string{"error": http.StatusText(status)})
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			res, err := l.Allow(c.Request().Context(), key(c.Request()))
			if err != nil && cfg.onError != nil {
				cfg.onError(c, err)
			}
			if err == nil {
				res.SetHeaders(c.Response().Header())
			}
			if status := limiter.Status(res, err); status != 0 {
				return cfg.deny(c, status, res)
			}
			return next(c)
		}
	}
}
