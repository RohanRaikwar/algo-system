package exitwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

const (
	reversalChannel   = "pub:analyst:reversal"
	reversalKey       = "analyst:reversal:latest"
	posContextChannel = "pub:poscontext"
	posContextKey     = "poscontext:latest"
	analystLevelsChan = "pub:analyst:levels"
	analystLevelsKey  = "analyst:levels:latest"
	ourSRChannel      = "pub:sr"
	ourSRKey          = "sr:state"
	signalChannel     = "pub:signal"

	evalEvery   = 250 * time.Millisecond // stall keeps growing between ticks
	candleEvery = 5 * time.Second        // poll for newly closed 1m index candles
	wantedEvery = 20                     // evalEvery ticks between token-set refreshes
	atrPeriod   = 14
)

// Service wires the Engine to Redis (ticks, poscontext, publish) and SQLite.
// All engine calls happen on the Run goroutine.
type Service struct {
	cfg     Config
	rdb     *goredis.Client
	rec     *Recorder
	journal *strategy.SignalJournal
	engine  *Engine

	wanted atomic.Pointer[map[string]bool] // tokens the engine needs; read by the tick subscriber

	tickCh   chan model.Tick
	posCh    chan []model.PositionContext
	candleCh chan model.TFCandle
	levelCh  chan levelUpdate
	sigCh    chan SignalEvent
	reloadCh chan Model
	pubCh    chan []byte
	latest   atomic.Pointer[[]byte]
}

type levelUpdate struct {
	token  string
	ourSR  bool // false: analyst levels
	levels []Level
}

// New connects to Redis and opens the recorder.
func New(cfg Config) (*Service, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	rec, err := OpenRecorder(cfg.DBPath)
	if err != nil {
		rdb.Close()
		return nil, err
	}
	svc := &Service{
		cfg: cfg, rdb: rdb, rec: rec,
		tickCh:   make(chan model.Tick, 4096),
		posCh:    make(chan []model.PositionContext, 8),
		candleCh: make(chan model.TFCandle, 512),
		levelCh:  make(chan levelUpdate, 16),
		sigCh:    make(chan SignalEvent, 256),
		reloadCh: make(chan Model, 1),
		pubCh:    make(chan []byte, 8),
	}
	svc.journal, err = strategy.NewSignalJournal(cfg.JournalPath)
	if err != nil {
		rdb.Close()
		return nil, fmt.Errorf("exitwatch journal: %w", err)
	}
	svc.engine = NewEngine(svc.loadModel(), svc, true)
	svc.engine.SetAutoExit(cfg.AutoExit)
	for _, k := range cfg.IndexKeys {
		exch, tok := splitKey(k)
		svc.engine.SetExchange(tok, exch)
	}
	svc.storeWanted()
	return svc, nil
}

func (svc *Service) loadModel() Model {
	p, err := LoadParams(svc.cfg.ParamsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("[exitwatch] no params file at %s, using defaults", svc.cfg.ParamsPath)
			return DefaultParams().Compile()
		}
		log.Printf("[exitwatch] params load error, using defaults: %v", err)
		return DefaultParams().Compile()
	}
	log.Printf("[exitwatch] params loaded from %s", svc.cfg.ParamsPath)
	return p.Compile()
}

// Run starts all subsystems and blocks until ctx is cancelled.
func (svc *Service) Run(ctx context.Context) error {
	log.Printf("[exitwatch] starting (shadow, except EXIT closes positions of %v via %s)", svc.cfg.AutoExit, model.ExitRequestChannel)

	recDone := make(chan struct{})
	recExited := make(chan struct{})
	go func() { svc.rec.Run(recDone); close(recExited) }()

	go heartbeat.NewPublisher("exitwatch", svc.rdb).Run(ctx)
	go svc.subscribeTicks(ctx)
	go svc.subscribePositions(ctx)
	go svc.subscribeLevels(ctx)
	go svc.candleLoop(ctx)
	go svc.publishLoop(ctx)
	go svc.signalLoop(ctx)
	svc.startHTTP(ctx)

	svc.loadInitialLevels(ctx)
	svc.loadInitialPositions(ctx)
	svc.loop(ctx)

	close(recDone)
	<-recExited
	svc.journal.Close()
	svc.rdb.Close()
	log.Println("[exitwatch] shutdown complete.")
	return nil
}

// loop is the only goroutine that touches the engine.
func (svc *Service) loop(ctx context.Context) {
	t := time.NewTicker(evalEvery)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case tk := <-svc.tickCh:
			svc.engine.OnTick(tk)
		case ps := <-svc.posCh:
			svc.engine.SetPositions(ps, svc.engine.ClockAt(time.Now()))
			svc.storeWanted()
		case c := <-svc.candleCh:
			svc.engine.OnCandle(c)
		case u := <-svc.levelCh:
			if u.ourSR {
				svc.engine.SetOurSRLevels(u.token, u.levels)
			} else {
				svc.engine.SetAnalystLevels(u.token, u.levels)
			}
		case m := <-svc.reloadCh:
			svc.engine.SetModel(m)
		case now := <-t.C:
			svc.engine.Tick(now)
			if n++; n%wantedEvery == 0 { // ghosts end inside Tick
				svc.storeWanted()
			}
		}
	}
}

// storeWanted publishes the token set for the tick subscriber: every
// tracker's tokens plus the index keys (day high/low for the level book).
// Called only from the loop goroutine.
func (svc *Service) storeWanted() {
	w := svc.engine.Tokens()
	for _, k := range svc.cfg.IndexKeys {
		_, tok := splitKey(k)
		w[tok] = true
	}
	svc.wanted.Store(&w)
}

func splitKey(k string) (exch, token string) {
	i := strings.LastIndexByte(k, ':')
	if i < 0 {
		return "NSE", k
	}
	return k[:i], k[i+1:]
}

// subscribeTicks decodes only ticks whose channel token the engine wants.
func (svc *Service) subscribeTicks(ctx context.Context) {
	ps := svc.rdb.PSubscribe(ctx, "pub:tick:*")
	defer ps.Close()
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			tok := msg.Channel[strings.LastIndexByte(msg.Channel, ':')+1:]
			if !(*svc.wanted.Load())[tok] {
				continue
			}
			var t model.Tick
			if err := json.Unmarshal([]byte(msg.Payload), &t); err != nil {
				continue
			}
			select {
			case svc.tickCh <- t:
			default: // engine behind; next tick supersedes
			}
		}
	}
}

func (svc *Service) subscribePositions(ctx context.Context) {
	ps := svc.rdb.Subscribe(ctx, posContextChannel)
	defer ps.Close()
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			svc.handlePositions(ctx, []byte(msg.Payload))
		}
	}
}

func (svc *Service) loadInitialPositions(ctx context.Context) {
	b, err := svc.rdb.Get(ctx, posContextKey).Bytes()
	if err != nil {
		if err != goredis.Nil {
			log.Printf("[exitwatch] read %s: %v", posContextKey, err)
		}
		return
	}
	svc.handlePositions(ctx, b)
}

func (svc *Service) handlePositions(ctx context.Context, b []byte) {
	var set model.PositionContextSet
	if err := json.Unmarshal(b, &set); err != nil {
		log.Printf("[exitwatch] bad poscontext payload: %v", err)
		return
	}
	select {
	case svc.posCh <- set.Positions:
	case <-ctx.Done():
	}
}

// candleLoop feeds closed 1m index candles to the engine: today's candles
// from 09:15 IST at startup, then new stream entries every candleEvery.
func (svc *Service) candleLoop(ctx context.Context) {
	last := make(map[string]string, len(svc.cfg.IndexKeys))
	for _, k := range svc.cfg.IndexKeys {
		y, m, d := time.Now().In(bookIST).Date()
		open := time.Date(y, m, d, 9, 15, 0, 0, bookIST)
		last[k] = fmt.Sprintf("%d-0", open.UnixMilli())
	}
	t := time.NewTicker(candleEvery)
	defer t.Stop()
	for {
		for _, k := range svc.cfg.IndexKeys {
			last[k] = svc.readCandles(ctx, k, last[k])
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// readCandles sends stream entries after from and returns the last ID read.
func (svc *Service) readCandles(ctx context.Context, key, from string) string {
	// from is the session open on the first read (inclusive), then
	// "(<last id>" (exclusive range start, Redis >= 6.2).
	msgs, err := svc.rdb.XRange(ctx, "candle:60s:"+key, from, "+").Result()
	if err != nil {
		log.Printf("[exitwatch] candle read %s: %v", key, err)
		return from
	}
	for _, msg := range msgs {
		data, ok := msg.Values["data"].(string)
		if !ok {
			continue
		}
		var c model.TFCandle
		if json.Unmarshal([]byte(data), &c) != nil || c.Forming {
			continue
		}
		select {
		case svc.candleCh <- c:
		case <-ctx.Done():
			return from
		}
	}
	if n := len(msgs); n > 0 {
		return "(" + msgs[n-1].ID
	}
	return from
}

// computeATR averages true range over the candles after the first.
func computeATR(cs []model.TFCandle) (int64, bool) {
	if len(cs) < 2 {
		return 0, false
	}
	var sum int64
	for i := 1; i < len(cs); i++ {
		hi, lo, pc := cs[i].High, cs[i].Low, cs[i-1].Close
		tr := hi - lo
		if d := hi - pc; d > tr {
			tr = d
		}
		if d := pc - lo; d > tr {
			tr = d
		}
		sum += tr
	}
	return sum / int64(len(cs)-1), true
}

// ── Sink ──

// Publish queues the payload for Redis; a full queue drops the oldest frame
// since each payload is full state.
func (svc *Service) Publish(p Payload) {
	b, err := json.Marshal(p)
	if err != nil {
		log.Printf("[exitwatch] payload marshal: %v", err)
		return
	}
	svc.latest.Store(&b)
	select {
	case svc.pubCh <- b:
	default:
		select {
		case <-svc.pubCh:
		default:
		}
		select {
		case svc.pubCh <- b:
		default:
		}
	}
}

func (svc *Service) RecordDecision(v View) { svc.rec.enqueue("decisions", v) }
func (svc *Service) RecordFeatures(v View) { svc.rec.enqueue("features_1hz", v) }

func (svc *Service) publishLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-svc.pubCh:
			if err := svc.rdb.Set(ctx, reversalKey, b, 24*time.Hour).Err(); err != nil {
				log.Printf("[exitwatch] store error: %v", err)
			}
			if err := svc.rdb.Publish(ctx, reversalChannel, string(b)).Err(); err != nil {
				log.Printf("[exitwatch] publish error: %v", err)
			}
		}
	}
}

// ── HTTP ──

func (svc *Service) startHTTP(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if b := svc.latest.Load(); b != nil {
			w.Write(*b)
			return
		}
		w.Write([]byte(`{"shadow":true,"positions":[]}`))
	})
	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		p, err := LoadParams(svc.cfg.ParamsPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case svc.reloadCh <- p.Compile():
		default:
			http.Error(w, "reload already pending", http.StatusConflict)
			return
		}
		log.Printf("[exitwatch] params reloaded from %s", svc.cfg.ParamsPath)
		w.Write([]byte("reloaded"))
	})
	srv := &http.Server{Addr: svc.cfg.HTTPAddr, Handler: mux}
	go func() {
		log.Printf("[exitwatch] HTTP on %s (/healthz, /state, /reload)", svc.cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[exitwatch] HTTP server error: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
}

// ── S/R level feeds ──

func (svc *Service) subscribeLevels(ctx context.Context) {
	ps := svc.rdb.Subscribe(ctx, analystLevelsChan, ourSRChannel)
	defer ps.Close()
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			svc.handleLevels(ctx, msg.Channel == ourSRChannel, []byte(msg.Payload))
		}
	}
}

func (svc *Service) loadInitialLevels(ctx context.Context) {
	for _, src := range []struct {
		key   string
		ourSR bool
	}{{analystLevelsKey, false}, {ourSRKey, true}} {
		b, err := svc.rdb.Get(ctx, src.key).Bytes()
		if err != nil {
			if err != goredis.Nil {
				log.Printf("[exitwatch] read %s: %v", src.key, err)
			}
			continue
		}
		svc.handleLevels(ctx, src.ourSR, b)
	}
}

func (svc *Service) handleLevels(ctx context.Context, ourSR bool, b []byte) {
	parse := parseAnalystLevels
	if ourSR {
		parse = parseOurSRLevels
	}
	tok, ls, err := parse(b)
	if err != nil {
		log.Printf("[exitwatch] %v", err)
		return
	}
	select {
	case svc.levelCh <- levelUpdate{token: tok, ourSR: ourSR, levels: ls}:
	case <-ctx.Done():
	}
}

// ExitRequest publishes an exit request for stratengine. Sent inline: it
// is rare and must not be dropped by a full queue.
func (svc *Service) ExitRequest(r model.ExitRequest) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.rdb.Publish(ctx, model.ExitRequestChannel, string(b)).Err(); err != nil {
		log.Printf("[exitwatch] exit request publish error: %v", err)
		return
	}
	log.Printf("[exitwatch] exit request sent: %s %s — %s", r.Strategy, r.Side, r.Reason)
}

// ── Signals (pub:signal + journal) ──

// Signal queues an exitwatch signal; a full queue drops it with a log line.
func (svc *Service) Signal(ev SignalEvent) {
	select {
	case svc.sigCh <- ev:
	default:
		log.Printf("[exitwatch] signal queue full, dropped %s %s", ev.Action, ev.Reason)
	}
}

func (svc *Service) signalLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-svc.sigCh:
			log.Printf("[exitwatch] signal %s %s %s: %s", ev.StrategyName, ev.Side, ev.Action, ev.Reason)
			if b, err := json.Marshal(ev); err == nil {
				if err := svc.rdb.Publish(ctx, signalChannel, string(b)).Err(); err != nil {
					log.Printf("[exitwatch] signal publish error: %v", err)
				}
			}
			sig := strategy.Signal{
				StrategyName: ev.StrategyName, Action: strategy.Action(ev.Action),
				Side: strategy.PositionSide(ev.Side), Token: ev.Token, Exchange: ev.Exchange,
				Price: ev.Price, Reason: ev.Reason, FNOToken: ev.FNOToken,
			}
			if err := svc.journal.Record(sig, ev.at, nil, false, false); err != nil {
				log.Printf("[exitwatch] journal error: %v", err)
			}
		}
	}
}
