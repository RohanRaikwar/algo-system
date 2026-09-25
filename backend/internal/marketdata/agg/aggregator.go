package agg

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
)

// candleState holds the in-progress candle for one instrument in the current second bucket.
type candleState struct {
	bucket int64  // Unix second of this bucket
	tok    string // "exchange:token" (its tokenClock); secondary keys carry a bucket suffix
	candle model.Candle
}

// tokenClock is one token's event-time clock. Watermarks are per token so a
// token whose exchange time runs ahead cannot make another token's ticks late.
type tokenClock struct {
	maxEventTS  int64     // newest bucket seen for the token (Unix seconds)
	wall        time.Time // wall time when maxEventTS last advanced
	finalBefore int64     // buckets < finalBefore were finalized by flushOld
	lastEmitted int64     // newest bucket emitted for the token; buckets <= it are final
	lastDayVol  int64     // newest cumulative day volume seen (volume baseline)
}

// tickVolume is the volume a tick adds to its candle. With a cumulative day
// volume (Angel Quote mode) it is the increase since the token's previous
// tick: Quote packets are conflated snapshots, so summing each packet's
// last-traded quantity would miss trades between packets. The first tick
// only sets the baseline (volume before we connected isn't this candle's),
// and a lower cumulative (reordered packet) adds nothing and keeps the
// baseline. Without a cumulative figure (LTP mode, staging) it falls back
// to the last traded quantity.
func (tc *tokenClock) tickVolume(t model.Tick) int64 {
	if t.DayVolume <= 0 {
		return t.Qty
	}
	prev := tc.lastDayVol
	if t.DayVolume <= prev {
		return 0
	}
	tc.lastDayVol = t.DayVolume
	if prev == 0 {
		return 0
	}
	return t.DayVolume - prev
}

// Aggregator builds 1-second OHLC candles from a stream of ticks.
// It runs in a single goroutine and emits finalized candles when the second rolls over.
//
// Event-time watermark: candles are finalized based on each token's event-time
// watermark (max event-time seen for that token minus ReorderBuffer, advancing
// with wall time while the token is idle), not a shared wall clock. This
// handles out-of-order ticks within the reorder window, and one token's clock
// cannot make another token's ticks late.
type Aggregator struct {
	mu     sync.Mutex
	states map[string]*candleState // key = "exchange:token"

	flushInterval time.Duration

	// ReorderBuffer is the duration to hold out-of-order ticks before
	// considering their bucket finalized. Default: 300ms.
	ReorderBuffer time.Duration

	// MarketCloseGate, when true, prevents new candle creation after 15:30 IST.
	// Post-close ticks merge into the last candle instead. Default: false.
	// Set to true in production (mdengine) to avoid spurious 15:30 candles.
	MarketCloseGate bool

	// Event-time watermark tracking, per token (key = "exchange:token").
	clocks    map[string]*tokenClock
	watermark int64 // newest per-token event watermark (observability only)

	// Metrics hooks (optional, set externally)
	OnDroppedTick func()               // called when candleCh is full
	OnLateTick    func()               // called when tick arrives behind watermark (event-time)
	OnCandle      func(c model.Candle) // called after a 1s candle is emitted to candleCh

	now func() time.Time // wall clock; overridable in tests
}

// New creates a new Aggregator with default settings.
func New() *Aggregator {
	return &Aggregator{
		states:        make(map[string]*candleState),
		clocks:        make(map[string]*tokenClock),
		flushInterval: 100 * time.Millisecond, // check frequency for bucket rollover
		ReorderBuffer: 300 * time.Millisecond, // default out-of-order tolerance
		now:           time.Now,
	}
}

// WatermarkDelay returns the current lag between wall-clock time and the
// event-time watermark. Useful for observability.
func (a *Aggregator) WatermarkDelay() time.Duration {
	a.mu.Lock()
	wm := a.watermark
	a.mu.Unlock()
	if wm == 0 {
		return 0
	}
	return time.Since(time.Unix(wm, 0))
}

// Run consumes ticks from tickCh in a single goroutine, aggregates into 1s candles,
// and sends finalized candles to candleCh. Blocks until ctx is cancelled.
func (a *Aggregator) Run(ctx context.Context, tickCh <-chan model.Tick, candleCh chan<- model.Candle) {
	ticker := time.NewTicker(a.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush any remaining open candles before exit
			a.flushAll(candleCh)
			return

		case tick, ok := <-tickCh:
			if !ok {
				a.flushAll(candleCh)
				return
			}
			a.processTick(tick, candleCh)

		case <-ticker.C:
			// Periodic flush: emit any candles whose bucket is behind the watermark
			a.flushOld(candleCh)
		}
	}
}

// processTick incorporates a single tick into the candle state.
// Uses event-time watermark to determine whether a tick is late.
// Post-close ticks (>=15:30 IST) merge into the last candle instead of starting new buckets.
func (a *Aggregator) processTick(tick model.Tick, candleCh chan<- model.Candle) {
	canonicalTS := tick.CanonicalTS()
	bucket := canonicalTS.Unix()
	key := tick.Exchange + ":" + tick.Token

	a.mu.Lock()
	defer a.mu.Unlock()

	// ── Market-close gate ──────────────────────────────────────────────
	// After 15:30 IST, ticks still arrive during the close detector's
	// grace period. Instead of creating a new candle at 15:30, merge the
	// tick into the last candle's Close/High/Low so the closing auction
	// price is captured correctly without producing a spurious candle.
	if a.MarketCloseGate && !markethours.IsMarketOpen(canonicalTS) {
		state, exists := a.states[key]
		if exists {
			c := &state.candle
			if tick.Price > c.High {
				c.High = tick.Price
			}
			if tick.Price < c.Low {
				c.Low = tick.Price
			}
			c.Close = tick.Price
			if tc := a.clocks[key]; tc != nil {
				c.Volume += tc.tickVolume(tick) // closing auction volume
			}
			c.TicksCount++
		}
		// If no existing candle, drop the tick (pre-open or post-close with no state)
		return
	}

	// Advance this token's watermark based on its max event-time seen
	tc := a.clocks[key]
	if tc == nil {
		tc = &tokenClock{}
		a.clocks[key] = tc
	}
	if bucket > tc.maxEventTS {
		tc.maxEventTS = bucket
		tc.wall = a.now()
		if wm := tc.maxEventTS - a.bufSec(); wm > a.watermark {
			a.watermark = wm
		}
	}
	watermark := tc.maxEventTS - a.bufSec()
	if tc.finalBefore > watermark {
		watermark = tc.finalBefore
	}

	// Drop ticks behind the watermark or for an already-emitted second —
	// their buckets are finalized (immutable) and must not be re-emitted
	if (watermark > 0 && bucket < watermark) || (tc.lastEmitted > 0 && bucket <= tc.lastEmitted) {
		cb := a.OnLateTick
		a.mu.Unlock()
		if cb != nil {
			cb()
		}
		a.mu.Lock()
		return
	}

	vol := tc.tickVolume(tick)
	state, exists := a.states[key]

	if exists && bucket < state.bucket {
		// Tick is for an older (but not yet flushed) bucket.
		// Use a bucket-keyed secondary entry to avoid unbounded key growth.
		secKey := key + ":" + strconv.FormatInt(bucket, 10)
		if secState, secExists := a.states[secKey]; secExists {
			// Merge into existing secondary bucket
			c := &secState.candle
			if tick.Price > c.High {
				c.High = tick.Price
			}
			if tick.Price < c.Low {
				c.Low = tick.Price
			}
			c.Close = tick.Price
			c.Volume += vol
			c.TicksCount++
		} else {
			a.states[secKey] = &candleState{
				bucket: bucket,
				tok:    key,
				candle: model.Candle{
					Token:      tick.Token,
					Exchange:   tick.Exchange,
					TS:         time.Unix(bucket, 0).UTC(),
					Open:       tick.Price,
					High:       tick.Price,
					Low:        tick.Price,
					Close:      tick.Price,
					Volume:     vol,
					TicksCount: 1,
				},
			}
		}
		return
	}

	if exists && bucket > state.bucket {
		// New bucket — finalize the old candle first
		a.emit(state, candleCh)
		delete(a.states, key)
		exists = false
	}

	if !exists {
		// Start a new candle for this bucket
		a.states[key] = &candleState{
			bucket: bucket,
			tok:    key,
			candle: model.Candle{
				Token:      tick.Token,
				Exchange:   tick.Exchange,
				TS:         time.Unix(bucket, 0).UTC(),
				Open:       tick.Price,
				High:       tick.Price,
				Low:        tick.Price,
				Close:      tick.Price,
				Volume:     vol,
				TicksCount: 1,
			},
		}
		return
	}

	// Same bucket — update OHLC
	c := &state.candle
	if tick.Price > c.High {
		c.High = tick.Price
	}
	if tick.Price < c.Low {
		c.Low = tick.Price
	}
	c.Close = tick.Price
	c.Volume += vol
	c.TicksCount++
}

// bufSec is the bucket-level reorder tolerance (whole seconds, minimum 1).
func (a *Aggregator) bufSec() int64 {
	bufSec := int64(a.ReorderBuffer.Seconds())
	if bufSec < 1 {
		bufSec = 1 // minimum 1 second granularity for bucket-level watermark
	}
	return bufSec
}

// flushOld emits candles for any bucket that is behind its token's event-time
// watermark. A token's clock advances with wall time since its last new
// bucket, so an idle token's last candle is still finalized.
func (a *Aggregator) flushOld(candleCh chan<- model.Candle) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	for key, state := range a.states {
		tc := a.clocks[state.tok]
		if tc == nil {
			// No clock (not expected): fall back to wall-clock time
			if state.bucket < now.Unix() {
				a.emit(state, candleCh)
				delete(a.states, key)
			}
			continue
		}
		wm := tc.maxEventTS + int64(now.Sub(tc.wall)/time.Second) - a.bufSec()
		if state.bucket < wm {
			a.emit(state, candleCh)
			delete(a.states, key)
			if wm > tc.finalBefore {
				tc.finalBefore = wm // later ticks for these buckets are late
			}
		}
	}
}

// FlushSession finalizes and emits all in-progress candles.
// Called at market close to ensure the last candle includes the closing price.
// Safe to call from any goroutine — uses internal mutex.
func (a *Aggregator) FlushSession(candleCh chan<- model.Candle) {
	a.flushAll(candleCh)
	a.mu.Lock()
	a.clocks = make(map[string]*tokenClock) // next session starts fresh clocks
	a.mu.Unlock()
	log.Println("[agg] session flushed — all forming candles finalized")
}

// flushAll emits all open candles regardless of bucket.
func (a *Aggregator) flushAll(candleCh chan<- model.Candle) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for key, state := range a.states {
		a.emit(state, candleCh)
		delete(a.states, key)
	}
}

// emit sends a finalized candle to candleCh. Non-blocking to avoid deadlocks.
func (a *Aggregator) emit(state *candleState, candleCh chan<- model.Candle) {
	if tc := a.clocks[state.tok]; tc != nil && state.bucket > tc.lastEmitted {
		tc.lastEmitted = state.bucket // final even if dropped below: never re-emit
	}
	select {
	case candleCh <- state.candle:
		if a.OnCandle != nil {
			a.OnCandle(state.candle)
		}
	default:
		if a.OnDroppedTick != nil {
			a.OnDroppedTick()
		}
		log.Printf("[agg] candleCh full, dropping candle %s ts=%v", state.candle.Key(), state.candle.TS)
	}
}
