package push

import (
	"context"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

const (
	signalChannel  = "pub:signal"
	refusedChannel = "pub:refused"

	backoffMin = 500 * time.Millisecond
	backoffMax = 30 * time.Second

	// queueSize bounds notifications waiting for delivery. Trade events are
	// rare; a full queue means push services are stuck, so new ones drop.
	queueSize = 64
)

// Service listens on pub:signal and pub:refused and pushes a notification
// for each BUY/SELL/EXIT/WATCH_EXIT signal and each new refused entry.
type Service struct {
	sender  *Sender
	rdb     *goredis.Client
	refused RefusedTracker
	queue   chan Notification
}

// NewService builds a Service that delivers through sender.
func NewService(sender *Sender, rdb *goredis.Client) *Service {
	return &Service{sender: sender, rdb: rdb, queue: make(chan Notification, queueSize)}
}

// Run blocks until ctx is cancelled. It returns at once if push is disabled.
func (svc *Service) Run(ctx context.Context) {
	if !svc.sender.Enabled() {
		log.Println("[push] VAPID keys not set; push notifications disabled")
		return
	}
	go svc.deliverLoop(ctx)

	backoff := backoffMin
	for ctx.Err() == nil {
		ps := svc.rdb.Subscribe(ctx, signalChannel, refusedChannel)
		if _, err := ps.Receive(ctx); err != nil {
			ps.Close()
			if ctx.Err() != nil {
				return
			}
			log.Printf("[push] subscribe failed: %v (retry in %s)", err, backoff)
			if !sleep(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		log.Println("[push] subscribed to pub:signal, pub:refused")
		backoff = backoffMin
		svc.route(ctx, ps.Channel())
		ps.Close()
	}
}

func (svc *Service) route(ctx context.Context, ch <-chan *goredis.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			switch msg.Channel {
			case signalChannel:
				if n, ok := FormatSignal([]byte(msg.Payload)); ok {
					svc.enqueue(n)
				}
			case refusedChannel:
				for _, n := range svc.refused.Next([]byte(msg.Payload)) {
					svc.enqueue(n)
				}
			}
		}
	}
}

func (svc *Service) enqueue(n Notification) {
	select {
	case svc.queue <- n:
	default:
		log.Printf("[push] queue full, dropped %q", n.Title)
	}
}

// deliverLoop sends one notification at a time so a slow push service never
// blocks the Redis subscription.
func (svc *Service) deliverLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-svc.queue:
			sent, err := svc.sender.Send(ctx, n)
			if err != nil {
				log.Printf("[push] send %q: %v", n.Title, err)
				continue
			}
			log.Printf("[push] %q delivered to %d device(s)", n.Title, sent)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
