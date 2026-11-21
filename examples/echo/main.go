package main

import (
	"log"
	"net/http"

	"github.com/alibazlamit/leaky_bucket_redis/v3/echolimiter"
	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
)

func main() {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	l, err := limiter.New(client, 1, limiter.WithBurst(5))
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(echolimiter.Middleware(l, limiter.ClientIP()))
	e.GET("/", func(c echo.Context) error { return c.String(http.StatusOK, "hello\n") })
	log.Fatal(e.Start(":8080"))
}
