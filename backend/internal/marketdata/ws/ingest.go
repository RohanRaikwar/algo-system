package ws

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"trading-systemv1/internal/model"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// exchangeTypeToName maps Angel One WS exchange_type ints to exchange name strings.
var exchangeTypeToName = map[int]string{
	1:  "NSE",
	2:  "NFO",
	3:  "BSE",
	4:  "BFO",
	5:  "MCX",
	7:  "NCX",
	13: "CDE",
}

// maxEventSkew bounds how far an exchange timestamp may be from local receipt
// time before it is considered bogus and replaced by the receive time. One bad
// timestamp would otherwise push the aggregator's shared watermark ahead and
// drop every other token's ticks as late.
const maxEventSkew = 5 * time.Second

// maxFutureSkew caps how far ahead of local receipt a plausible event time may
// be (EventTS = min(EventTS, TickTS+maxFutureSkew)). It matches the
// aggregator's 1s bucket-level reorder tolerance, so a token whose clock runs
// ahead cannot open buckets that real time has not reached.
const maxFutureSkew = 1 * time.Second

// ErrFeedLost is returned by Start when the socket gave up (auth rejected or
// reconnects exhausted); the caller should re-login and start a new Ingest.
var ErrFeedLost = errors.New("ws ingest: feed lost")

// IngestConfig holds configuration for the WS ingest.
type IngestConfig struct {
	AuthToken  string
	APIKey     string
	ClientCode string
	FeedToken  string

	// Tokens to subscribe, grouped by exchange type and mode.
	SubscribeMode int
	TokenList     []smartconnect.TokenListEntry
}

// Ingest connects to Angel One WebSocket and pushes normalized ticks into tickCh.
type Ingest struct {
	cfg IngestConfig
	ws  *smartconnect.SmartWebSocketV3

	// Optional metrics hooks
	OnReconnect func()
	OnTick      func(token string, price int64)  // called with token and LTP on every tick (for close detection)
	OnIngested  func(tick *model.Tick)           // called for every parsed tick (TicksTotal, E2E latency)
	OnDrop      func()                           // called when tickCh is full and the tick is dropped
	OnSeqGap    func(token string, missed int64) // called when a feed sequence gap is detected
	OnClockSkew func()                           // called when an implausible exchange timestamp was replaced
	OnConnState func(connected bool)             // called on every socket open/close

	seq   *seqTracker
	fatal chan error // socket gave up; ends Start
}

// New creates a new Ingest instance.
func New(cfg IngestConfig) (*Ingest, error) {
	ws, err := smartconnect.NewSmartWebSocketV3(
		cfg.AuthToken,
		cfg.APIKey,
		cfg.ClientCode,
		cfg.FeedToken,
		5, // maxRetryAttempt (warning only; reconnect keeps trying)
		1, // retryStrategy: exponential (capped at 60s)
		5, // retryDelaySec
		2, // retryMultiplier
		5, // retryDurationMin: then give up so the session re-logins
	)
	if err != nil {
		return nil, fmt.Errorf("ws ingest: create websocket: %w", err)
	}

	return &Ingest{cfg: cfg, ws: ws, seq: newSeqTracker(), fatal: make(chan error, 1)}, nil
}

// Start connects to the WebSocket and begins streaming ticks into tickCh.
// Blocks until ctx is cancelled (returns nil) or the socket gives up
// (returns an error wrapping ErrFeedLost).
func (ing *Ingest) Start(ctx context.Context, tickCh chan<- model.Tick) error {
	ing.wire(tickCh)

	if err := ing.ws.Connect(); err != nil {
		return fmt.Errorf("%w: connect: %v", ErrFeedLost, err)
	}

	select {
	case <-ctx.Done():
		ing.ws.CloseConnection()
		return nil
	case err := <-ing.fatal:
		ing.ws.CloseConnection()
		return err
	}
}

// wire installs the socket callbacks.
func (ing *Ingest) wire(tickCh chan<- model.Tick) {
	ing.ws.AddSubscription(ing.cfg.SubscribeMode, ing.cfg.TokenList)

	ing.ws.OnOpen = func() {
		if ing.OnConnState != nil {
			ing.OnConnState(true)
		}
		// Angel sequence numbers may restart on a new connection; forget prior state.
		ing.seq.reset()
		// The socket sends every stored subscription (configured list plus
		// dynamic tokens, incl. ones requested while it was down) right after
		// OnOpen, on every connect.
		log.Printf("[ws] connected, subscribing mode=%d tokens=%+v (+ stored dynamic tokens)", ing.cfg.SubscribeMode, ing.cfg.TokenList)
	}

	ing.ws.OnData = func(msg map[string]interface{}) {
		recvTS := time.Now().UTC()
		tick, err := parseTick(msg, recvTS)
		if err != nil {
			log.Printf("[ws] parse error: %v", err)
			return
		}
		if sanitizeEventTS(&tick) && ing.OnClockSkew != nil {
			ing.OnClockSkew()
		}

		if seqNo := toInt64(msg["sequence_number"]); seqNo > 0 {
			if missed := ing.seq.observe(tick.Token, seqNo); missed > 0 {
				if ing.OnSeqGap != nil {
					ing.OnSeqGap(tick.Token, missed)
				}
				if ing.seq.shouldLog(recvTS) {
					log.Printf("[ws] feed sequence gap: token=%s missed=%d seq=%d", tick.Token, missed, seqNo)
				}
			}
		}

		if ing.OnIngested != nil {
			ing.OnIngested(&tick)
		}

		// Notify close detector (price observation for market close detection)
		if ing.OnTick != nil {
			ing.OnTick(tick.Token, tick.Price)
		}

		select {
		case tickCh <- tick:
		default:
			if ing.OnDrop != nil {
				ing.OnDrop()
			}
		}
	}

	ing.ws.OnClose = func() {
		log.Println("[ws] connection closed")
		if ing.OnConnState != nil {
			ing.OnConnState(false)
		}
		if ing.OnReconnect != nil {
			ing.OnReconnect()
		}
	}

	ing.ws.OnError = func(code, msg string) {
		log.Printf("[ws] error: code=%s msg=%s", code, msg)
		if code == smartconnect.ErrCodeAuthRejected || code == smartconnect.ErrCodeRetryExhausted {
			select {
			case ing.fatal <- fmt.Errorf("%w: %s: %s", ErrFeedLost, code, msg):
			default:
			}
		}
	}
}

// sanitizeEventTS replaces an exchange timestamp further than maxEventSkew from
// local receipt with the receipt time, and caps a plausible future one at
// receipt + maxFutureSkew. Reports whether it changed EventTS.
func sanitizeEventTS(tick *model.Tick) bool {
	if tick.EventTS.IsZero() {
		return false
	}
	d := tick.EventTS.Sub(tick.TickTS)
	switch {
	case d > maxEventSkew || d < -maxEventSkew:
		tick.EventTS = tick.TickTS
	case d > maxFutureSkew:
		tick.EventTS = tick.TickTS.Add(maxFutureSkew)
	default:
		return false
	}
	return true
}

// parseTick converts the raw WS message map into a model.Tick.
// recvTS is the local frame-receipt time (TickTS); the exchange timestamp,
// when present, becomes EventTS (the canonical time used for bucketing).
func parseTick(msg map[string]interface{}, recvTS time.Time) (model.Tick, error) {
	token, _ := msg["token"].(string)
	if token == "" {
		return model.Tick{}, fmt.Errorf("missing token")
	}

	exType := toInt(msg["exchange_type"])
	exchange := exchangeTypeToName[exType]
	if exchange == "" {
		exchange = fmt.Sprintf("EX_%d", exType)
	}

	price := toInt64(msg["last_traded_price"])
	qty := toInt64(msg["last_traded_quantity"])
	dayVol := toInt64(msg["volume_trade_for_the_day"]) // Quote/SnapQuote only

	// Exchange timestamp (epoch ms from Angel One) is the canonical event time.
	var eventTS time.Time
	if exTS := toInt64(msg["exchange_timestamp"]); exTS > 0 {
		eventTS = time.Unix(0, exTS*int64(time.Millisecond)).UTC()
	}

	return model.Tick{
		Token:     token,
		Exchange:  exchange,
		Price:     price,
		Qty:       qty,
		DayVolume: dayVol,
		TickTS:    recvTS,
		EventTS:   eventTS,
	}, nil
}

// seqGapLogInterval rate-limits feed gap log lines (the metric counts every gap).
const seqGapLogInterval = 5 * time.Second

// sequenceGap returns how many sequence numbers were skipped between last and cur.
// last == 0 means no prior observation. Duplicates and backwards jumps (feed
// restart) are not gaps.
func sequenceGap(last, cur int64) int64 {
	if last == 0 || cur <= last+1 {
		return 0
	}
	return cur - last - 1
}

// seqTracker holds the last sequence number seen per token.
type seqTracker struct {
	mu      sync.Mutex
	last    map[string]int64
	lastLog time.Time
}

func newSeqTracker() *seqTracker {
	return &seqTracker{last: make(map[string]int64)}
}

// observe records seq for token and returns the number of missed sequence numbers.
func (t *seqTracker) observe(token string, seq int64) int64 {
	t.mu.Lock()
	gap := sequenceGap(t.last[token], seq)
	t.last[token] = seq
	t.mu.Unlock()
	return gap
}

// reset forgets all sequence state (call on every (re)connect).
func (t *seqTracker) reset() {
	t.mu.Lock()
	t.last = make(map[string]int64)
	t.mu.Unlock()
}

// shouldLog reports whether a gap log line may be emitted at now.
func (t *seqTracker) shouldLog(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.lastLog) < seqGapLogInterval {
		return false
	}
	t.lastLog = now
	return true
}

func toInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}

// ForceReconnect ends Start with ErrFeedLost so the session loop re-logins
// and builds a new socket (used by the stale-feed watchdog). Non-blocking.
func (ing *Ingest) ForceReconnect(reason string) {
	select {
	case ing.fatal <- fmt.Errorf("%w: forced reconnect: %s", ErrFeedLost, reason):
	default: // a fatal error is already pending
	}
}

// LastFrameTime is when the socket last connected or received any frame
// (data, ping, pong); the stale-feed watchdog uses it to tell a dead socket
// from a live one that carries no ticks.
func (ing *Ingest) LastFrameTime() time.Time {
	return ing.ws.LastFrameTime()
}

// SubscribeTokens dynamically subscribes additional tokens on the live WS connection.
// This is used to add FNO CE/PE tokens after ATM strike resolution at market open.
func (ing *Ingest) SubscribeTokens(mode int, tokens []smartconnect.TokenListEntry) error {
	if ing.ws == nil {
		return fmt.Errorf("ws not connected")
	}
	log.Printf("[ws] subscribing dynamic tokens: mode=%d tokens=%+v", mode, tokens)
	return ing.ws.Subscribe("dynamic_fno", mode, tokens)
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}
