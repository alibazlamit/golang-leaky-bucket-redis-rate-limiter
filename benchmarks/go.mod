module github.com/alibazlamit/leaky_bucket_redis/benchmarks

go 1.25.0

require (
	github.com/alibazlamit/leaky_bucket_redis/v3 v3.0.0
	github.com/go-redis/redis_rate/v10 v10.0.1
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)

replace github.com/alibazlamit/leaky_bucket_redis/v3 => ../
