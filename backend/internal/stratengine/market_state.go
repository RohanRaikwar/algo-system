package stratengine

import (
	"context"
	"log"
	"time"
)

// marketStateChannel carries "open"/"closed", published by mdengine
// (internal/store/redis/writer.go PublishMarketState) on every WS connect —
// so an intraday reconnect counts too, not only the 09:15 open.
const marketStateChannel = "pub:market:state"

// onMarketState handles one payload from marketStateChannel. On "open" it
// resets the global option picker's session and the old strike ladder so
// both re-subscribe their full contract list: a fresh WS connection forgets
// mdengine's dynamic subscriptions (see rememberDynamicTokens), so an
// intraday reconnect otherwise leaves the ladder/universe believing
// contracts are still streamed when the socket no longer has them. The
// picker's own midnight date-change reset (refreshUniverseAt) remains a
// fallback for a process that runs across midnight without a reconnect.
// A small, Redis-free method so the handler is unit-tested without a
// broker.
func (svc *Service) onMarketState(state string) {
	if state != "open" {
		return
	}
	if svc.picker != nil {
		svc.picker.NewSession()
	}
	svc.resetLadderSession()
}

// resetLadderSession forgets the old strike ladder's subscriptions and
// centre so refreshStrikeLadder resubscribes it in full on the next NIFTY
// tick. Guarded by ladderBusy, the same flag refreshStrikeLadder uses to
// keep ladderSubscribed touched by one goroutine at a time; this call is
// off the hot path (a rare market-state event), so it waits rather than
// skipping the reset.
func (svc *Service) resetLadderSession() {
	for !svc.ladderBusy.CompareAndSwap(false, true) {
		time.Sleep(time.Millisecond)
	}
	svc.ladderSubscribed = nil
	svc.ladderCenter.Store(0)
	svc.ladderBusy.Store(false)
}

// marketStateLoop follows pub:market:state, calling onMarketState on every
// message and resubscribing if the subscription drops (same pattern as
// configUpdateLoop).
func (svc *Service) marketStateLoop(ctx context.Context) {
	client := svc.redisWriter.Client()
	backoff := time.Second
	for ctx.Err() == nil {
		sub := client.Subscribe(ctx, marketStateChannel)
		for msg := range sub.Channel() {
			backoff = time.Second
			svc.onMarketState(msg.Payload)
		}
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("[stratengine] ⚠️  %s subscription closed — resubscribing in %s", marketStateChannel, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
