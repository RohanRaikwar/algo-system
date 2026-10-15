package main

import (
	"bufio"
	"context"
	"io"

	goredis "github.com/go-redis/redis/v8"
)

// RecordTicks subscribes to pattern (pub:tick:* by default) and writes each
// payload as one JSONL line. mdengine's PublishTick payload is model.Tick
// JSON, so the file is directly replayable. Messages published while the
// subscription is down are lost (Redis PubSub is fire-and-forget).
func RecordTicks(ctx context.Context, addr, password, pattern string, w io.Writer) (int, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: addr, Password: password})
	defer rdb.Close()

	ps := rdb.PSubscribe(ctx, pattern)
	defer ps.Close()
	if _, err := ps.Receive(ctx); err != nil {
		return 0, err
	}

	bw := bufio.NewWriter(w)
	defer bw.Flush()
	n := 0
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return n, ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return n, nil
			}
			if _, err := bw.WriteString(msg.Payload); err != nil {
				return n, err
			}
			if err := bw.WriteByte('\n'); err != nil {
				return n, err
			}
			n++
			if n%1000 == 0 {
				_ = bw.Flush()
			}
		}
	}
}
