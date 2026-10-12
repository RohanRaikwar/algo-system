package omsvc

import (
	"context"
	"fmt"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// Leader lock: two OMS instances (e.g. overlapping deploys) must never
// both act on the same intents. The lock holds the instance id with a TTL
// and is renewed well inside it; renew and release only touch the lock
// while this instance still owns it.
var (
	renewLock   = goredis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) else return 0 end`)
	releaseLock = goredis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`)
)

// acquireLeader blocks until this instance holds key or ctx ends.
func acquireLeader(ctx context.Context, rdb *goredis.Client, key, id string, ttl time.Duration) error {
	logged := false
	for {
		ok, err := rdb.SetNX(ctx, key, id, ttl).Result()
		if err == nil && ok {
			return nil
		}
		if !logged {
			holder, _ := rdb.Get(ctx, key).Result()
			log.Printf("[oms] ⏳ waiting for leader lock %s (held by %q, err=%v)", key, holder, err)
			logged = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ttl / 3):
		}
	}
}

// holdLeader renews the lock every ttl/3 until ctx ends, then releases
// it. It calls lost and returns if the lock is taken over or cannot be
// renewed before it would expire.
func holdLeader(ctx context.Context, rdb *goredis.Client, key, id string, ttl time.Duration, lost func()) {
	t := time.NewTicker(ttl / 3)
	defer t.Stop()
	lastOK := time.Now()
	for {
		select {
		case <-ctx.Done():
			rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			releaseLock.Run(rctx, rdb, []string{key}, id)
			cancel()
			return
		case <-t.C:
			n, err := renewLock.Run(ctx, rdb, []string{key}, id, ttl.Milliseconds()).Int()
			switch {
			case err == nil && n == 1:
				lastOK = time.Now()
			case err == nil:
				log.Printf("[oms] 🚨 leader lock %s taken over — stopping", key)
				lost()
				return
			case time.Since(lastOK) > ttl*2/3:
				log.Printf("[oms] 🚨 leader lock %s not renewed (%v) — stopping before it expires", key, err)
				lost()
				return
			}
		}
	}
}

func instanceID(consumer string) string {
	return fmt.Sprintf("%s-%d", consumer, time.Now().UnixNano())
}
