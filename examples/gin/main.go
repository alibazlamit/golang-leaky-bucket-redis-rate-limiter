package main

import (
	"log"
	"net/http"

	"github.com/alibazlamit/leaky_bucket_redis/v3/ginlimiter"
	"github.com/alibazlamit/leaky_bucket_redis/v3/limiter"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func main() {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	l, err := limiter.New(client, 1, limiter.WithBurst(5))
	if err != nil {
		log.Fatal(err)
	}

	r := gin.New()
	r.Use(ginlimiter.Middleware(l, limiter.ClientIP()))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "hello\n") })
	log.Fatal(r.Run(":8080"))
}
