package omsvc

import (
	"time"

	goredis "github.com/go-redis/redis/v8"
)

func unreachableRedis() *goredis.Client {
	return goredis.NewClient(&goredis.Options{
		Addr:        "127.0.0.1:1", // nothing listens here
		DialTimeout: 50 * time.Millisecond,
		MaxRetries:  -1,
	})
}
