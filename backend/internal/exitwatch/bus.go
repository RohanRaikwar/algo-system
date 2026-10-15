package exitwatch

import (
	"context"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// busMsg is one pub/sub message.
type busMsg struct {
	Channel string
	Payload string
}

// streamEntry is one Redis stream entry's "data" field.
type streamEntry struct {
	ID   string
	Data string
}

// bus is everything exitwatch needs from Redis. The service depends on this
// interface; redisBus is the only production implementation, and tests use
// an in-memory fake.
type bus interface {
	// Subscribe and PSubscribe deliver messages until ctx is cancelled or
	// the returned close func is called; the channel is then closed.
	Subscribe(ctx context.Context, channels ...string) (<-chan busMsg, func())
	PSubscribe(ctx context.Context, pattern string) (<-chan busMsg, func())
	Publish(ctx context.Context, channel, payload string) error
	// Get returns ok=false (and no error) when the key does not exist.
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// XRange reads stream entries from start (inclusive, or "(id" exclusive)
	// to the end.
	XRange(ctx context.Context, stream, start string) ([]streamEntry, error)
}

// redisBus adapts a go-redis client to bus.
type redisBus struct{ c *goredis.Client }

func (b redisBus) Subscribe(ctx context.Context, channels ...string) (<-chan busMsg, func()) {
	return relay(ctx, b.c.Subscribe(ctx, channels...))
}

func (b redisBus) PSubscribe(ctx context.Context, pattern string) (<-chan busMsg, func()) {
	return relay(ctx, b.c.PSubscribe(ctx, pattern))
}

// relay copies go-redis messages into a busMsg channel with the same
// buffering go-redis uses (100).
func relay(ctx context.Context, ps *goredis.PubSub) (<-chan busMsg, func()) {
	out := make(chan busMsg, 100)
	in := ps.Channel()
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- busMsg{Channel: m.Channel, Payload: m.Payload}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, func() { ps.Close() }
}

func (b redisBus) Publish(ctx context.Context, channel, payload string) error {
	return b.c.Publish(ctx, channel, payload).Err()
}

func (b redisBus) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := b.c.Get(ctx, key).Bytes()
	if err == goredis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (b redisBus) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return b.c.Set(ctx, key, val, ttl).Err()
}

func (b redisBus) XRange(ctx context.Context, stream, start string) ([]streamEntry, error) {
	msgs, err := b.c.XRange(ctx, stream, start, "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]streamEntry, 0, len(msgs))
	for _, m := range msgs {
		data, _ := m.Values["data"].(string)
		out = append(out, streamEntry{ID: m.ID, Data: data})
	}
	return out, nil
}
