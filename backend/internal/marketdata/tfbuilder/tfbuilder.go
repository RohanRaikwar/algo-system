// Package tfbuilder provides an incremental timeframe resampler.
// It consumes finalized 1-second candles and maintains "forming" TF candle
// states that are updated in O(1) per candle per TF. When a TF bucket
// closes (i.e., a candle arrives in a new bucket), the previous TF candle
// is finalized and emitted.
package tfbuilder

import (
	"context"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// tfState holds the forming candle state for one (token, TF) pair.
type tfState struct {
	bucket  int64 // bucket start = ts - ts%tf (Unix seconds)
	candle  model.TFCandle
	started bool
}

// Builder resamples 1s candles into multiple dynamic timeframes.
// Goroutine-safe: designed to run in a single goroutine (single consumer).
type Builder struct {
	mu  sync.Mutex // protects states when timer flush runs concurrently
	tfs []int      // enabled TF durations in seconds

	// Per-TF per-token state.
	// Key structure: states[tfIdx][tokenKey] → *tfState
	states []map[string]*tfState

	// Staleness validation: reject candles older than bucket_start - tolerance.
	// Default: 2s. Set to 0 to disable.
	StaleTolerance time.Duration

	// FlushGrace adds a grace period (in seconds, event time) before a TF bucket
	// is finalized without its own next-bucket candle. It covers the spread
	// between tokens' 1s candles leaving the aggregator (~1.5s watermark delay).
	// Default: 2s.
	FlushGrace int64

	// Event-time clocks, per token (key = candle Key()). 1s candles are
	// bucketed by exchange time, so a token's TF buckets are finalized off the
	// newest 1s candle seen for that token, not the local wall clock (a
	// lagging feed would otherwise lose each bucket's last seconds) and not
	// another token's clock (one token running ahead would finalize the
	// others early). Between candles a clock advances with elapsed wall time
	// so an idle token still finalizes.
	clocks map[string]*eventClock

	// Metrics hooks
	OnTFCandle    func(c model.TFCandle) // called on finalized TF candle (optional)
	OnStaleCandle func()                 // called when a stale candle is rejected (optional)
	OnLateCandle  func()                 // called when a 1s candle arrives for an already-finalized bucket (optional)
	OnDrop        func(final bool)       // called when a TF candle could not be emitted (optional)

	// FinalOut, when set, receives finalized TF candles; the outCh passed to
	// the methods then carries only forming snapshots. Finals are sent
	// blocking (never dropped for a full channel) until Done is closed.
	FinalOut chan<- model.TFCandle
	Done     <-chan struct{}

	now func() time.Time // wall clock; overridable in tests
}

// eventClock is one token's event-time clock.
type eventClock struct {
	maxEventTS int64     // newest 1s candle TS seen (Unix seconds)
	wall       time.Time // wall time when maxEventTS last advanced
}

// eventNow returns key's event-time clock (Unix seconds): its newest 1s
// candle TS plus wall time elapsed since it arrived. Zero before any candle.
func (b *Builder) eventNow(key string) int64 {
	c := b.clocks[key]
	if c == nil || c.maxEventTS == 0 {
		return 0
	}
	return c.maxEventTS + int64(b.now().Sub(c.wall)/time.Second)
}

// New creates a TF builder with the given timeframes (in seconds).
func New(tfs []int) *Builder {
	states := make([]map[string]*tfState, len(tfs))
	for i := range states {
		states[i] = make(map[string]*tfState, 64) // preallocate for ~64 tokens
	}
	return &Builder{
		tfs:            tfs,
		states:         states,
		clocks:         make(map[string]*eventClock, 64),
		StaleTolerance: 2 * time.Second, // default: reject candles > 2s stale
		FlushGrace:     2,               // default: 2s grace for aggregator watermark delay
		now:            time.Now,
	}
}

// UpdateTFs dynamically updates the enabled timeframes.
// Existing forming candles for removed TFs are finalized and emitted.
func (b *Builder) UpdateTFs(newTFs []int, outCh chan<- model.TFCandle) {
	// Build set of new TFs
	newSet := make(map[int]bool, len(newTFs))
	for _, tf := range newTFs {
		newSet[tf] = true
	}

	// Finalize forming candles for TFs being removed
	for i, tf := range b.tfs {
		if !newSet[tf] {
			for _, st := range b.states[i] {
				if st.started {
					st.candle.Forming = false
					b.emit(outCh, st.candle)
				}
			}
		}
	}

	// Rebuild states: keep existing states for TFs that persist, add new ones
	oldStates := make(map[int]map[string]*tfState, len(b.tfs))
	for i, tf := range b.tfs {
		oldStates[tf] = b.states[i]
	}

	b.tfs = newTFs
	b.states = make([]map[string]*tfState, len(newTFs))
	for i, tf := range newTFs {
		if old, ok := oldStates[tf]; ok {
			b.states[i] = old
		} else {
			b.states[i] = make(map[string]*tfState, 64)
		}
	}
}

// Run consumes 1s candles from candleCh, resamples them into TF candles,
// and sends finalized TF candles to outCh. Blocks until ctx is cancelled.
func (b *Builder) Run(ctx context.Context, candleCh <-chan model.Candle, outCh chan<- model.TFCandle) {
	for {
		select {
		case <-ctx.Done():
			b.flushAll(outCh)
			return
		case c, ok := <-candleCh:
			if !ok {
				b.flushAll(outCh)
				return
			}
			b.process(c, outCh)
		}
	}
}

// Process handles a single 1s candle against all enabled TFs.
// This is the hot path — O(1) per TF.
// Caller must hold b.mu when using RunWithTimer.
func (b *Builder) process(c model.Candle, outCh chan<- model.TFCandle) {
	ts := c.TS.Unix()
	key := c.Key()

	clk := b.clocks[key]
	if clk == nil {
		clk = &eventClock{}
		b.clocks[key] = clk
	}
	// Re-anchor only when the candle is ahead of the running clock, so the
	// clock never moves backwards and a late candle cannot re-open a bucket
	// that the wall-advanced clock already finalized.
	if ts > b.eventNow(key) {
		clk.maxEventTS = ts
		clk.wall = b.now()
		// This token's event time moved: finalize its buckets now past
		// end + grace (other tokens follow their own clocks / the timer).
		b.flushExpiredKey(key, outCh)
	}

	for i, tf := range b.tfs {
		tf64 := int64(tf)
		bucket := ts - (ts % tf64) // align to TF boundary

		st, exists := b.states[i][key]

		// Staleness check: reject candles whose bucket is behind the
		// current forming bucket by more than StaleTolerance.
		// This prevents late/out-of-order candles from corrupting
		// an already-advancing bucket.
		if b.StaleTolerance > 0 && exists && bucket < st.bucket {
			lag := time.Duration(st.bucket-bucket) * time.Second
			if lag > b.StaleTolerance {
				if b.OnStaleCandle != nil {
					b.OnStaleCandle()
				}
				continue // skip this TF for the stale candle
			}
		}

		if exists && bucket > st.bucket {
			// New bucket — finalize the forming candle
			st.candle.Forming = false
			b.emit(outCh, st.candle)
			if b.OnTFCandle != nil {
				b.OnTFCandle(st.candle)
			}
			exists = false
		}

		if !exists {
			// This bucket was already finalized by the event clock: the 1s
			// candle is too late. Don't create an orphan state for it.
			if b.eventNow(key) >= bucket+tf64+b.FlushGrace {
				if b.OnLateCandle != nil {
					b.OnLateCandle()
				}
				continue
			}

			// Start a new forming candle for this bucket
			newState := &tfState{
				bucket:  bucket,
				started: true,
				candle: model.TFCandle{
					Token:    c.Token,
					Exchange: c.Exchange,
					TF:       tf,
					TS:       time.Unix(bucket, 0).UTC(),
					Open:     c.Open,
					High:     c.High,
					Low:      c.Low,
					Close:    c.Close,
					Volume:   c.Volume,
					Count:    1,
					Forming:  true,
				},
			}
			b.states[i][key] = newState
			// Emit immediately so live-preview pipeline sees the first tick.
			snap := newState.candle
			b.emit(outCh, snap)
			continue
		}

		// Same bucket — merge OHLCV (O(1))
		fc := &st.candle
		if c.High > fc.High {
			fc.High = c.High
		}
		if c.Low < fc.Low {
			fc.Low = c.Low
		}
		fc.Close = c.Close
		fc.Volume += c.Volume
		fc.Count++

		// Emit a forming snapshot so the live-preview pipeline can peek at
		// the in-progress candle every second.  We copy the struct to avoid
		// a race if the caller holds onto the value after the next tick.
		snap := *fc // shallow copy is safe (no pointer fields)
		b.emit(outCh, snap)
	}
}

// flushExpired finalizes any forming candles whose TF bucket end + FlushGrace
// has passed on their token's event clock. This eliminates the dependency on
// the next bucket's first candle for finalization.
// Caller must hold b.mu.
func (b *Builder) flushExpired(outCh chan<- model.TFCandle) {
	for i := range b.tfs {
		for key := range b.states[i] {
			b.flushExpiredState(i, key, outCh)
		}
	}
}

// flushExpiredKey is flushExpired for one token. Caller must hold b.mu.
func (b *Builder) flushExpiredKey(key string, outCh chan<- model.TFCandle) {
	for i := range b.tfs {
		b.flushExpiredState(i, key, outCh)
	}
}

// flushExpiredState finalizes states[i][key] if its bucket end + grace has
// passed on the token's event clock. Caller must hold b.mu.
func (b *Builder) flushExpiredState(i int, key string, outCh chan<- model.TFCandle) {
	st, ok := b.states[i][key]
	if !ok {
		return
	}
	if !st.started {
		delete(b.states[i], key) // clean up already-finalized state
		return
	}
	now := b.eventNow(key)
	if now == 0 {
		return
	}
	// Bucket has elapsed: event clock is past bucket_start + TF + grace
	if now >= st.bucket+int64(b.tfs[i])+b.FlushGrace {
		st.candle.Forming = false
		b.emit(outCh, st.candle)
		if b.OnTFCandle != nil {
			b.OnTFCandle(st.candle)
		}
		delete(b.states[i], key) // remove so process() won't re-emit
	}
}

// FlushSession finalizes and emits all forming TF candles.
// Called at market close to ensure last candles include the closing price.
func (b *Builder) FlushSession(outCh chan<- model.TFCandle) {
	b.mu.Lock()
	b.flushAll(outCh)
	b.clocks = make(map[string]*eventClock, 64) // next session starts fresh event clocks
	b.mu.Unlock()
	log.Println("[tfbuilder] session flushed — all forming TF candles finalized")
}

// flushAll finalizes and emits all forming candles.
func (b *Builder) flushAll(outCh chan<- model.TFCandle) {
	for i := range b.tfs {
		for key, st := range b.states[i] {
			if st.started {
				st.candle.Forming = false
				b.emit(outCh, st.candle)
			}
			delete(b.states[i], key)
		}
	}
}

// emit sends a TF candle out. Finals go to FinalOut when set, blocking until
// delivered or Done closes (a final candle is data, not a preview). Otherwise
// the send is non-blocking to avoid deadlocks; every drop is reported.
func (b *Builder) emit(outCh chan<- model.TFCandle, c model.TFCandle) {
	final := !c.Forming
	if final && b.FinalOut != nil {
		select {
		case b.FinalOut <- c:
			return
		case <-b.Done:
		}
	} else {
		select {
		case outCh <- c:
			return
		default:
		}
	}
	if b.OnDrop != nil {
		b.OnDrop(final)
	}
	if final {
		log.Printf("[tfbuilder] dropping final TF candle %s tf=%d ts=%v", c.Key(), c.TF, c.TS)
	}
}

// TFs returns the current list of enabled timeframes.
func (b *Builder) TFs() []int {
	return b.tfs
}

// Run1 processes a single 1s candle against all TFs (hot path).
// This avoids channel overhead when called inline from the pipeline.
// NOTE: When using RunWithTimer, use Run1Locked instead.
func (b *Builder) Run1(c model.Candle, outCh chan<- model.TFCandle) {
	b.process(c, outCh)
}

// Run1Locked processes a single 1s candle with mutex protection.
// Use this when RunWithTimer is active (timer goroutine shares state).
func (b *Builder) Run1Locked(c model.Candle, outCh chan<- model.TFCandle) {
	b.mu.Lock()
	b.process(c, outCh)
	b.mu.Unlock()
}

// RunWithTimer starts a background goroutine that flushes expired TF buckets
// every 500ms on the event clock (see eventNow). This finalizes candles even
// if no new data arrives.
//
// The caller must use Run1Locked (not Run1) for candle processing when this
// method is active, since the timer goroutine shares state.
//
// Blocks until ctx is cancelled.
func (b *Builder) RunWithTimer(ctx context.Context, outCh chan<- model.TFCandle) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			b.flushAll(outCh)
			b.mu.Unlock()
			return
		case <-ticker.C:
			b.mu.Lock()
			b.flushExpired(outCh)
			b.mu.Unlock()
		}
	}
}
