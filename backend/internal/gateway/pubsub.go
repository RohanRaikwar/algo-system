package gateway

import (
	"context"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

var dynamicPubSubPatterns = []string{
	"pub:ind:*",
	"pub:tick:*",
	"pub:analyst:*",
	"pub:strike",
	"pub:orders",
	"pub:signal",
	"pub:pnl",
}

// PubSubRouter manages Redis PubSub subscriptions and routes messages
// to the broadcaster for fan-out to WebSocket clients.
type PubSubRouter struct {
	hub *Hub
}

// NewPubSubRouter creates a PubSubRouter backed by the given Hub.
func NewPubSubRouter(hub *Hub) *PubSubRouter {
	return &PubSubRouter{hub: hub}
}

const (
	pubsubBackoffMin = 500 * time.Millisecond
	pubsubBackoffMax = 30 * time.Second
)

// pubsubConn is one open subscription. ch carries *goredis.Message and
// *goredis.Subscription values; confirmed lists channels/patterns whose
// subscription confirmation was consumed while opening it.
type pubsubConn struct {
	ch        <-chan interface{}
	close     func() error
	confirmed []string
}

// pubsubSource opens a subscription. Injected so the resubscribe loop can be
// tested without Redis.
type pubsubSource func(ctx context.Context) (pubsubConn, error)

// redisSource adapts a go-redis subscribe call into a pubsubSource. It waits
// for the subscription confirmation so a dead Redis surfaces as an error.
//
// go-redis v8 reconnects a PubSub internally and Channel() only closes on
// Close(), so a Redis blip is invisible there. ChannelWithSubscriptions also
// delivers the re-sent subscription confirmations, which route() uses to
// detect the reconnect.
func redisSource(subscribe func(ctx context.Context) *goredis.PubSub) pubsubSource {
	return func(ctx context.Context) (pubsubConn, error) {
		ps := subscribe(ctx)
		first, err := ps.Receive(ctx)
		if err != nil {
			ps.Close()
			return pubsubConn{}, err
		}
		var confirmed []string
		if sub, ok := first.(*goredis.Subscription); ok {
			confirmed = append(confirmed, sub.Channel)
		}
		return pubsubConn{ch: ps.ChannelWithSubscriptions(ctx, 100), close: ps.Close, confirmed: confirmed}, nil
	}
}

// RunExplicit subscribes to explicitly listed channels and routes messages.
// Resubscribes with backoff if the subscription dies. Blocks until ctx is cancelled.
func (r *PubSubRouter) RunExplicit(ctx context.Context) {
	channels := r.hub.buildChannels()
	if len(channels) == 0 {
		log.Println("[api_gateway] WARNING: no explicit channels to subscribe to")
		return
	}

	log.Printf("[api_gateway] subscribing to %d PubSub channels", len(channels))
	src := redisSource(func(ctx context.Context) *goredis.PubSub {
		return r.hub.Rdb.Subscribe(ctx, channels...)
	})
	r.runSubscription(ctx, "explicit", src, pubsubBackoffMin, pubsubBackoffMax)
}

// RunPattern subscribes to wildcard patterns for dynamic indicator channels.
// Resubscribes with backoff if the subscription dies. Blocks until ctx is cancelled.
func (r *PubSubRouter) RunPattern(ctx context.Context) {
	src := redisSource(func(ctx context.Context) *goredis.PubSub {
		return r.hub.Rdb.PSubscribe(ctx, dynamicPubSubPatterns...)
	})
	r.runSubscription(ctx, "pattern", src, pubsubBackoffMin, pubsubBackoffMax)
}

// runSubscription routes messages from src to the broadcaster, resubscribing
// with exponential backoff whenever the subscription fails or its channel
// closes. Every successful resubscribe requests a hub epoch bump: messages published
// during the outage were lost, so clients must discard seqs and resync.
func (r *PubSubRouter) runSubscription(ctx context.Context, name string, src pubsubSource, minBackoff, maxBackoff time.Duration) {
	backoff := minBackoff
	subscribedBefore := false

	for ctx.Err() == nil {
		conn, err := src(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[api_gateway] PubSub %s subscribe failed: %v (retry in %s)", name, err, backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		if subscribedBefore {
			log.Printf("[api_gateway] PubSub %s resubscribed", name)
			r.hub.requestEpochBump("pubsub " + name + " resubscribed")
		} else {
			log.Printf("[api_gateway] PubSub %s subscribed", name)
		}
		subscribedBefore = true
		backoff = minBackoff

		closed := r.route(ctx, name, conn)
		conn.close()
		if !closed {
			return // ctx cancelled
		}
		log.Printf("[api_gateway] WARNING: PubSub %s channel closed; resubscribing in %s", name, backoff)
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, maxBackoff)
	}
}

// route forwards messages until ctx is done (returns false) or the channel
// closes (returns true). A subscription confirmation for a channel that was
// already confirmed means go-redis reconnected internally and messages may have
// been lost, so it requests an (debounced) epoch bump.
func (r *PubSubRouter) route(ctx context.Context, name string, conn pubsubConn) bool {
	seen := make(map[string]bool, len(conn.confirmed))
	for _, c := range conn.confirmed {
		seen[c] = true
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case v, ok := <-conn.ch:
			if !ok {
				return true
			}
			switch msg := v.(type) {
			case *goredis.Message:
				r.hub.broadcast(msg.Channel, []byte(msg.Payload))
			case *goredis.Subscription:
				if msg.Kind != "subscribe" && msg.Kind != "psubscribe" {
					continue
				}
				if seen[msg.Channel] {
					log.Printf("[api_gateway] PubSub %s reconnected (re-confirmed %s)", name, msg.Channel)
					r.hub.requestEpochBump("pubsub " + name + " reconnected")
				}
				seen[msg.Channel] = true
			}
		}
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	cur *= 2
	if cur > max {
		return max
	}
	return cur
}

// sleepCtx sleeps for d, returning false if ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
