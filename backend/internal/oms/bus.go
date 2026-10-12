package oms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// Bus carries intents and order events over Redis streams.
type Bus struct {
	rdb *goredis.Client
}

// NewBus wraps a Redis client.
func NewBus(rdb *goredis.Client) *Bus { return &Bus{rdb: rdb} }

// PublishIntent appends an intent to IntentStream.
func (b *Bus) PublishIntent(ctx context.Context, in Intent) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return b.rdb.XAdd(ctx, &goredis.XAddArgs{
		Stream: IntentStream,
		MaxLen: IntentStreamMaxLen,
		Approx: true,
		Values: map[string]any{"id": in.ID, "data": string(data)},
	}).Err()
}

// PublishEvent appends an order event (any JSON-encodable value) to
// EventStream.
func (b *Bus) PublishEvent(ctx context.Context, kind string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.rdb.XAdd(ctx, &goredis.XAddArgs{
		Stream: EventStream,
		MaxLen: EventStreamMaxLen,
		Approx: true,
		Values: map[string]any{"kind": kind, "data": string(data)},
	}).Err()
}

// ConsumeIntents reads IntentStream as consumer in group until ctx ends.
// Entries this consumer read but never acknowledged (a crash mid-handle)
// are handled first, then new ones. Each entry is acknowledged after
// handle returns, whatever it returns: handle owns retries and dedupe, and
// an entry that can never succeed must not block the stream.
func (b *Bus) ConsumeIntents(ctx context.Context, group, consumer string, handle func(Intent) error) error {
	err := b.rdb.XGroupCreateMkStream(ctx, IntentStream, group, "$").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("oms: create group %s: %w", group, err)
	}
	start := "0" // own pending entries first
	for ctx.Err() == nil {
		res, err := b.rdb.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{IntentStream, start},
			Count:    16,
			Block:    2 * time.Second,
		}).Result()
		if errors.Is(err, goredis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("[oms] ⚠️  read %s: %v", IntentStream, err)
			time.Sleep(time.Second)
			continue
		}
		n := 0
		for _, s := range res {
			for _, msg := range s.Messages {
				n++
				in, derr := decodeIntent(msg.Values)
				if derr != nil {
					log.Printf("[oms] ⚠️  bad intent %s: %v — skipped", msg.ID, derr)
				} else if herr := handle(in); herr != nil {
					log.Printf("[oms] ⚠️  intent %s (%s %s %s): %v", in.ID, in.Strategy, in.Action, in.Side, herr)
				}
				if err := b.rdb.XAck(ctx, IntentStream, group, msg.ID).Err(); err != nil {
					log.Printf("[oms] ⚠️  ack %s: %v", msg.ID, err)
				}
			}
		}
		if start == "0" && n == 0 {
			start = ">" // pending drained; switch to new entries
		}
	}
	return nil
}

func decodeIntent(v map[string]any) (Intent, error) {
	raw, ok := v["data"].(string)
	if !ok {
		return Intent{}, errors.New("no data field")
	}
	var in Intent
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return Intent{}, err
	}
	if in.ID == "" {
		return Intent{}, errors.New("intent without id")
	}
	return in, nil
}
