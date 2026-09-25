package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"trading-systemv1/internal/model"

	goredis "github.com/go-redis/redis/v8"
)

const (
	// Stream trimming: ~3h of 1s candles + buffer
	stream1sMaxLen   = 12000
	defaultLatestTTL = 30 * time.Minute

	// Hot-path write budgets: a stalled Redis must not block tick ingest.
	tickPublishTimeout = 200 * time.Millisecond
	candleWriteTimeout = 200 * time.Millisecond

	// Final TF candles are lossless data (strategies consume the streams):
	// each attempt is bounded, and failed attempts are retried.
	tfCandleWriteTimeout  = 500 * time.Millisecond
	tfCandleWriteAttempts = 3
	tfCandleRetryBackoff  = 100 * time.Millisecond
)

// Operation labels passed to Writer.OnWriteError.
const (
	OpTick     = "tick"
	OpCandle1s = "candle_1s"
	OpTFCandle = "tf_candle"
)

// WriterConfig configures the Redis writer.
type WriterConfig struct {
	Addr     string // Redis address, e.g. "localhost:6379"
	Password string
	DB       int
}

// Writer writes candles, TF candles, and indicator results to Redis.
type Writer struct {
	client *goredis.Client

	// OnWriteError is called (optional) when a bounded hot-path write fails
	// or times out. op is one of the Op* constants.
	OnWriteError func(op string)

	// OnDuplicateTFCandle is called (optional) when a final TF candle's
	// stream ID is already taken by a different payload; it is not written.
	OnDuplicateTFCandle func()

	// TFHistory (optional) returns recent final TF candles from durable
	// storage (SQLite), oldest first. RunTFCandles uses it on start, and
	// after its outage buffer overflowed, to write the finals the streams
	// are missing before any newer candle, so an outage leaves no gap.
	TFHistory func(ctx context.Context) ([]model.TFCandle, error)
}

// Final TF candles that could not be written wait in RunTFCandles' outage
// buffer (in arrival order) and are retried every tfOutageRetryEvery. A
// stream is append-only, so nothing newer may be written ahead of them.
var (
	tfOutageRetryEvery = 2 * time.Second
	tfOutageBufferMax  = 50000
)

// Client returns the underlying Redis client for health checks.
func (w *Writer) Client() *goredis.Client { return w.client }

// NewUnchecked creates a Writer without contacting the server. Use it when
// the first ping failed: the client reconnects on its own, and the writers
// retry, so Redis coming back later needs no process restart.
func NewUnchecked(cfg WriterConfig) *Writer {
	return &Writer{client: goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})}
}

// New creates a new Redis Writer and pings the server.
func New(cfg WriterConfig) (*Writer, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	log.Printf("[redis] connected to %s", cfg.Addr)
	return &Writer{client: client}, nil
}

// Run reads 1s candles from candleCh and writes them to Redis.
// Blocks until ctx is cancelled or candleCh is closed.
func (w *Writer) Run(ctx context.Context, candleCh <-chan model.Candle) {
	for {
		select {
		case <-ctx.Done():
			return
		case candle, ok := <-candleCh:
			if !ok {
				return
			}
			w.writeCandle(ctx, candle)
		}
	}
}

// RunTFCandles reads TF candles and writes them to Redis Streams.
// Blocks until ctx is cancelled or channel is closed.
//
// A final candle that cannot be written (Redis down) is kept, with every
// candle after it, in an in-order outage buffer that is retried until Redis
// is back — never dropped, never overtaken. Before the first write, and
// again if the buffer ever overflowed, the streams are topped up from
// TFHistory.
func (w *Writer) RunTFCandles(ctx context.Context, tfCandleCh <-chan model.TFCandle) {
	var pending []model.TFCandle
	synced := w.TFHistory == nil
	outage := false
	retry := time.NewTicker(tfOutageRetryEvery)
	defer retry.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-tfCandleCh:
			if !ok {
				return
			}
			pending = append(pending, tfc)
			if len(pending) > tfOutageBufferMax {
				// Oldest go; TFHistory refills them once Redis is back.
				drop := len(pending) - tfOutageBufferMax
				pending = append(pending[:0:0], pending[drop:]...)
				synced = w.TFHistory == nil
				log.Printf("[redis] TF outage buffer full — dropped %d oldest finals (to be restored from history)", drop)
			}
			if outage {
				continue // buffered; the retry tick tries Redis again
			}
		case <-retry.C:
			if len(pending) == 0 && synced {
				continue
			}
		}

		if !synced {
			if err := w.syncTFHistory(ctx, pending); err != nil {
				outage = true
				continue
			}
			synced = true
		}
		for len(pending) > 0 {
			if err := w.writeTFCandleErr(ctx, pending[0]); err != nil {
				if !outage {
					log.Printf("[redis] ⚠️  TF candle writes failing — buffering finals in order until Redis is back")
				}
				outage = true
				break
			}
			pending = pending[1:]
		}
		if outage && len(pending) == 0 {
			outage = false
			log.Printf("[redis] ✅ TF candle writes recovered — outage buffer drained")
		}
	}
}

// syncTFHistory writes the final candles from TFHistory that a stream is
// missing: newer than the stream's top entry and older than the first of
// that stream's candles still pending (those follow from the buffer).
func (w *Writer) syncTFHistory(ctx context.Context, pending []model.TFCandle) error {
	hist, err := w.TFHistory(ctx)
	if err != nil {
		// History unreadable: nothing to top up from; don't hold live data.
		log.Printf("[redis] TF history unavailable for stream sync: %v", err)
		return nil
	}
	firstPending := make(map[string]time.Time)
	for _, c := range pending {
		if _, ok := firstPending[c.StreamKey()]; !ok {
			firstPending[c.StreamKey()] = c.TS
		}
	}
	tops := make(map[string]time.Time)
	written := 0
	for _, c := range hist {
		c.Forming = false
		sk := c.StreamKey()
		if fp, ok := firstPending[sk]; ok && !c.TS.Before(fp) {
			continue
		}
		top, ok := tops[sk]
		if !ok {
			cctx, cancel := context.WithTimeout(ctx, tfCandleWriteTimeout)
			top, _, err = w.streamTop(cctx, sk)
			cancel()
			if err != nil {
				return err
			}
			tops[sk] = top
		}
		if !c.TS.After(top) {
			continue
		}
		if err := w.writeTFCandleErr(ctx, c); err != nil {
			return err
		}
		tops[sk] = c.TS
		written++
	}
	if written > 0 {
		log.Printf("[redis] ✅ restored %d final TF candles into streams from history", written)
	}
	return nil
}

// RunFormingTFCandles publishes forming TF candles via PubSub ONLY (no XADD).
// Used for live/streaming indicator peek updates every second.
// OPTIMIZED: uses string concat instead of fmt.Sprintf.
func (w *Writer) RunFormingTFCandles(ctx context.Context, ch <-chan model.TFCandle) {
	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-ch:
			if !ok {
				return
			}
			jsonBytes := tfc.JSON()
			jsonData := *(*string)(unsafe.Pointer(&jsonBytes))
			pubsubCh := "pub:candle:" + itoa(tfc.TF) + "s:" + tfc.Exchange + ":" + tfc.Token
			w.client.Publish(ctx, pubsubCh, jsonData)
		}
	}
}

// RunIndicators reads indicator results and writes them to Redis Streams.
// Blocks until ctx is cancelled or channel is closed.
func (w *Writer) RunIndicators(ctx context.Context, indCh <-chan model.IndicatorResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case ind, ok := <-indCh:
			if !ok {
				return
			}
			w.writeIndicator(ctx, ind)
		}
	}
}

// WriteIndicatorBatch writes multiple indicator results in a single Redis pipeline.
// This batches XADD + SET + PUBLISH for all results into one network roundtrip.
// Optimized: uses pre-built channel names, []byte→string zero-copy, no fmt.Sprintf.
func (w *Writer) WriteIndicatorBatch(ctx context.Context, results []model.IndicatorResult) {
	if len(results) == 0 {
		return
	}

	pipe := w.client.Pipeline()
	for i := range results {
		ind := &results[i]
		if !ind.Ready && !ind.Live {
			continue
		}

		jsonBytes := ind.JSON()
		// Zero-copy []byte→string (safe: jsonBytes is not mutated after this)
		jsonData := *(*string)(unsafe.Pointer(&jsonBytes))
		pubsubCh := ind.PubSubChannel()

		if ind.Live {
			pipe.Publish(ctx, pubsubCh, jsonData)
			continue
		}

		// Confirmed: XADD + SET + PUBLISH
		streamKey := ind.StreamKey()
		maxLen := int64(10800/ind.TF) + 100
		if maxLen < 200 {
			maxLen = 200
		}
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: streamKey,
			MaxLen: maxLen,
			Approx: true,
			Values: map[string]interface{}{"data": jsonData},
		})
		latestKey := "ind:" + ind.Name + ":" + itoa(ind.TF) + "s:latest:" + ind.Exchange + ":" + ind.Token
		pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)
		pipe.Publish(ctx, pubsubCh, jsonData)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] indicator batch pipeline error (%d results): %v", len(results), err)
	}
}

// PublishFormingBatch publishes multiple forming TF candles in a single pipeline.
func (w *Writer) PublishFormingBatch(ctx context.Context, candles []model.TFCandle) {
	if len(candles) == 0 {
		return
	}

	pipe := w.client.Pipeline()
	for _, tfc := range candles {
		jsonData := string(tfc.JSON())
		pubsubCh := fmt.Sprintf("pub:candle:%ds:%s:%s", tfc.TF, tfc.Exchange, tfc.Token)
		pipe.Publish(ctx, pubsubCh, jsonData)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] forming batch pipeline error (%d candles): %v", len(candles), err)
	}
}

// LoadTFRegistry reads the tf:enabled set from Redis.
// Returns empty slice if key doesn't exist.
func (w *Writer) LoadTFRegistry(ctx context.Context) ([]int, error) {
	members, err := w.client.SMembers(ctx, "tf:enabled").Result()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis SMEMBERS tf:enabled: %w", err)
	}

	tfs := make([]int, 0, len(members))
	for _, m := range members {
		n := 0
		for _, c := range m {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 {
			tfs = append(tfs, n)
		}
	}
	return tfs, nil
}

// writeCandle performs pipelined writes for a 1s candle.
func (w *Writer) writeCandle(ctx context.Context, candle model.Candle) {
	latestKey := fmt.Sprintf("candle:1s:latest:%s:%s", candle.Exchange, candle.Token)
	streamKey := fmt.Sprintf("candle:1s:%s:%s", candle.Exchange, candle.Token)
	pubsubCh := fmt.Sprintf("pub:candle:1s:%s:%s", candle.Exchange, candle.Token)
	jsonData := string(candle.JSON())

	ctx, cancel := context.WithTimeout(ctx, candleWriteTimeout)
	defer cancel()

	pipe := w.client.Pipeline()

	// SET latest candle with TTL
	pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)

	// XADD to stream with auto-trimming (~3h window)
	pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey,
		MaxLen: stream1sMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"data": jsonData,
		},
	})

	// PUBLISH to pubsub channel
	pipe.Publish(ctx, pubsubCh, jsonData)

	_, err := pipe.Exec(ctx)
	if err != nil {
		if w.OnWriteError != nil {
			w.OnWriteError(OpCandle1s)
		}
		log.Printf("[redis] pipeline error for %s: %v", candle.Key(), err)
	}
}

// writeTFCandle publishes a final TF candle to its Redis Stream, SETs latest
// and PUBLISHes it. Each attempt is bounded by tfCandleWriteTimeout; failed
// attempts are retried (tfCandleWriteAttempts) and a final failure is reported
// via OnWriteError(OpTFCandle). The XADD uses an explicit ID derived from the
// candle TS, so a retry after an ambiguous timeout cannot duplicate the entry.
func (w *Writer) writeTFCandle(ctx context.Context, tfc model.TFCandle) {
	_ = w.writeTFCandleErr(ctx, tfc)
}

// writeTFCandleErr is writeTFCandle reporting the final error.
func (w *Writer) writeTFCandleErr(ctx context.Context, tfc model.TFCandle) error {
	var err error
	for attempt := 1; attempt <= tfCandleWriteAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt-1) * tfCandleRetryBackoff):
			}
		}
		if err = w.tryWriteTFCandle(ctx, tfc); err == nil {
			return nil
		}
	}
	if w.OnWriteError != nil {
		w.OnWriteError(OpTFCandle)
	}
	log.Printf("[redis] TF candle write failed after %d attempts for %s tf=%d ts=%v: %v",
		tfCandleWriteAttempts, tfc.Key(), tfc.TF, tfc.TS, err)
	return err
}

// tfCandleStreamID is the explicit stream ID for a TF candle: "<bucket start ms>-0".
// TF buckets per stream are finalized in order, so IDs are monotonic.
func tfCandleStreamID(tfc *model.TFCandle) string {
	return strconv.FormatInt(tfc.TS.UnixMilli(), 10) + "-0"
}

// isStreamIDTooSmall reports Redis' XADD rejection of an ID <= the stream top.
func isStreamIDTooSmall(err error) bool {
	return err != nil && strings.Contains(err.Error(), "equal or smaller than the target stream top item")
}

func (w *Writer) tryWriteTFCandle(ctx context.Context, tfc model.TFCandle) error {
	ctx, cancel := context.WithTimeout(ctx, tfCandleWriteTimeout)
	defer cancel()

	streamKey := tfc.StreamKey()
	// Proportional MAXLEN: 3h of TF candles = 10800/TF + buffer
	maxLen := int64(10800/tfc.TF) + 100
	if maxLen < 200 {
		maxLen = 200
	}

	jsonData := string(tfc.JSON())
	xadd := &goredis.XAddArgs{
		Stream: streamKey,
		ID:     tfCandleStreamID(&tfc),
		MaxLen: maxLen,
		Approx: true,
		Values: map[string]interface{}{
			"data": jsonData,
		},
	}

	// XADD alone first: SET/PUBLISH must not run for a stale candle.
	err := w.client.XAdd(ctx, xadd).Err()
	if isStreamIDTooSmall(err) {
		top, topData, terr := w.streamTop(ctx, streamKey)
		if terr != nil {
			return terr
		}
		switch {
		case top.Equal(tfc.TS) && topData == jsonData:
			err = nil // already written (retry of an ambiguous attempt)
		case top.Equal(tfc.TS):
			// Same bucket already finalised with a different payload (a
			// tail-only re-finalisation, or a partial after a restart
			// mid-bucket): keep the stored candle as latest; don't publish.
			if w.OnDuplicateTFCandle != nil {
				w.OnDuplicateTFCandle()
			}
			log.Printf("[redis] TF candle %s tf=%d ts=%v already written with a different payload: skipped", tfc.Key(), tfc.TF, tfc.TS)
			return nil
		case top.After(tfc.TS):
			log.Printf("[redis] TF candle %s tf=%d ts=%v older than stream top %v: skipped", tfc.Key(), tfc.TF, tfc.TS, top)
			return nil
		default:
			// Top ID was auto-generated (wall clock, pre-explicit-ID stream):
			// append with an auto ID so IDs stay monotonic.
			xadd.ID = ""
			err = w.client.XAdd(ctx, xadd).Err()
		}
	}
	if err != nil {
		return err
	}

	pipe := w.client.Pipeline()

	// SET latest TF candle
	latestKey := fmt.Sprintf("candle:%ds:latest:%s:%s", tfc.TF, tfc.Exchange, tfc.Token)
	pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)

	// PUBLISH for real-time subscribers
	pubsubCh := fmt.Sprintf("pub:candle:%ds:%s:%s", tfc.TF, tfc.Exchange, tfc.Token)
	pipe.Publish(ctx, pubsubCh, jsonData)

	_, err = pipe.Exec(ctx)
	return err
}

// streamTop returns the candle TS and raw payload of the stream's newest entry.
func (w *Writer) streamTop(ctx context.Context, streamKey string) (time.Time, string, error) {
	msgs, err := w.client.XRevRangeN(ctx, streamKey, "+", "-", 1).Result()
	if err != nil {
		return time.Time{}, "", err
	}
	if len(msgs) == 0 {
		return time.Time{}, "", nil
	}
	data, _ := msgs[0].Values["data"].(string)
	var top struct {
		TS time.Time `json:"ts"`
	}
	if err := json.Unmarshal([]byte(data), &top); err != nil {
		return time.Time{}, "", fmt.Errorf("stream top of %s: %w", streamKey, err)
	}
	return top.TS, data, nil
}

// writeIndicator publishes an indicator result to its Redis Stream.
func (w *Writer) writeIndicator(ctx context.Context, ind model.IndicatorResult) {
	if !ind.Ready && !ind.Live {
		return // skip not-ready confirmed indicators
	}

	jsonBytes := ind.JSON()
	jsonData := *(*string)(unsafe.Pointer(&jsonBytes))
	pubsubCh := ind.PubSubChannel()

	if ind.Live {
		// Live/preview results: PubSub only (no XADD streams, no SET latest)
		w.client.Publish(ctx, pubsubCh, jsonData)
		return
	}

	// Confirmed results: full pipeline (XADD + SET + PUBLISH)
	streamKey := ind.StreamKey()
	pipe := w.client.Pipeline()

	// XADD to indicator stream (keep ~3h worth)
	maxLen := int64(10800/ind.TF) + 100
	if maxLen < 200 {
		maxLen = 200
	}
	pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey,
		MaxLen: maxLen,
		Approx: true,
		Values: map[string]interface{}{
			"data": jsonData,
		},
	})

	// SET latest indicator value
	latestKey := "ind:" + ind.Name + ":" + itoa(ind.TF) + "s:latest:" + ind.Exchange + ":" + ind.Token
	pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)

	// PUBLISH for real-time subscribers (dashboard)
	pipe.Publish(ctx, pubsubCh, jsonData)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] indicator pipeline error for %s: %v", ind.Name, err)
	}
}

// PublishMarketState sets the market state key in Redis and publishes a notification.
// state should be "open" or "closed". Downstream services use this to distinguish
// expected idle from pipeline failure (ADR-006).
func (w *Writer) PublishMarketState(state string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pipe := w.client.Pipeline()
	pipe.Set(ctx, "market:state", state, 24*time.Hour)
	pipe.Publish(ctx, "pub:market:state", state)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[redis] failed to publish market state %q: %v", state, err)
	} else {
		log.Printf("[redis] market state → %s", state)
	}
}

// PublishTick publishes a raw tick to PubSub for live stoploss checking.
// Channel format: pub:tick:<exchange>:<token>
// Bounded by tickPublishTimeout so a stalled Redis cannot block the tick router.
func (w *Writer) PublishTick(ctx context.Context, tick model.Tick) error {
	jsonData := string(tick.JSON())
	pubsubCh := "pub:tick:" + tick.Exchange + ":" + tick.Token
	ctx, cancel := context.WithTimeout(ctx, tickPublishTimeout)
	err := w.client.Publish(ctx, pubsubCh, jsonData).Err()
	cancel()
	if err != nil && w.OnWriteError != nil {
		w.OnWriteError(OpTick)
	}
	return err
}

// PublishTicks publishes a batch of raw ticks in one pipeline (one round trip,
// one deadline). Same channels as PublishTick. The slice is not retained.
func (w *Writer) PublishTicks(ctx context.Context, ticks []model.Tick) error {
	if len(ticks) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, tickPublishTimeout)
	defer cancel()
	pipe := w.client.Pipeline()
	for i := range ticks {
		t := &ticks[i]
		pipe.Publish(ctx, "pub:tick:"+t.Exchange+":"+t.Token, string(t.JSON()))
	}
	_, err := pipe.Exec(ctx)
	if err != nil && w.OnWriteError != nil {
		w.OnWriteError(OpTick)
	}
	return err
}

// Close closes the Redis client.
func (w *Writer) Close() error {
	return w.client.Close()
}
