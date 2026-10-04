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
)

const (
	reversalChannel   = "pub:analyst:reversal"
	reversalKey       = "analyst:reversal:latest"
	posContextChannel = "pub:poscontext"
	posContextKey     = "poscontext:latest"

	evalEvery = 250 * time.Millisecond // stall keeps growing between ticks
	atrEvery  = 30 * time.Second
	atrPeriod = 14
)

// Service wires the Engine to Redis (ticks, poscontext, publish) and SQLite.
// All engine calls happen on the Run goroutine.
type Service struct {
	cfg    Config
	rdb    *goredis.Client
	rec    *Recorder
	engine *Engine

	wanted atomic.Pointer[map[string]bool] // tokens the engine needs; read by the tick subscriber

	tickCh   chan model.Tick
	posCh    chan []model.PositionContext
	atrCh    chan atrUpdate
	reloadCh chan Model
	pubCh    chan []byte
	latest   atomic.Pointer[[]byte]
}

type atrUpdate struct {
	token string
	atr   int64
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
		atrCh:    make(chan atrUpdate, 8),
		reloadCh: make(chan Model, 1),
		pubCh:    make(chan []byte, 8),
	}
	empty := map[string]bool{}
	svc.wanted.Store(&empty)
	svc.engine = NewEngine(svc.loadModel(), svc, true)
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
	log.Println("[exitwatch] starting (shadow mode: publishes and records only, never orders)")

	recDone := make(chan struct{})
	recExited := make(chan struct{})
	go func() { svc.rec.Run(recDone); close(recExited) }()

	go heartbeat.NewPublisher("exitwatch", svc.rdb).Run(ctx)
	go svc.subscribeTicks(ctx)
	go svc.subscribePositions(ctx)
	go svc.atrLoop(ctx)
	go svc.publishLoop(ctx)
	svc.startHTTP(ctx)

	svc.loadInitialPositions(ctx)
	svc.loop(ctx)

	close(recDone)
	<-recExited
	svc.rdb.Close()
	log.Println("[exitwatch] shutdown complete.")
	return nil
}

// loop is the only goroutine that touches the engine.
func (svc *Service) loop(ctx context.Context) {
	t := time.NewTicker(evalEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case tk := <-svc.tickCh:
			svc.engine.OnTick(tk)
		case ps := <-svc.posCh:
			svc.engine.SetPositions(ps, time.Now())
			svc.storeWanted(ps)
		case u := <-svc.atrCh:
			svc.engine.SetATR(u.token, u.atr)
		case m := <-svc.reloadCh:
			svc.engine.SetModel(m)
		case now := <-t.C:
			svc.engine.Tick(now)
		}
	}
}

func (svc *Service) storeWanted(ps []model.PositionContext) {
	w := make(map[string]bool, 2*len(ps))
	for _, p := range ps {
		w[p.IndexToken] = true
		if p.FNOToken != "" {
			w[p.FNOToken] = true
		}
	}
	svc.wanted.Store(&w)
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

// atrLoop refreshes a simple 14-bar 1m ATR per index from the closed-candle stream.
func (svc *Service) atrLoop(ctx context.Context) {
	t := time.NewTicker(atrEvery)
	defer t.Stop()
	for {
		for _, key := range svc.cfg.IndexKeys {
			if atr, ok := svc.fetchATR(ctx, key); ok {
				select {
				case svc.atrCh <- atrUpdate{token: key[strings.LastIndexByte(key, ':')+1:], atr: atr}:
				case <-ctx.Done():
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (svc *Service) fetchATR(ctx context.Context, key string) (int64, bool) {
	msgs, err := svc.rdb.XRevRangeN(ctx, "candle:60s:"+key, "+", "-", atrPeriod+1).Result()
	if err != nil {
		log.Printf("[exitwatch] ATR read %s: %v", key, err)
		return 0, false
	}
	cs := make([]model.TFCandle, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- { // chronological
		data, ok := msgs[i].Values["data"].(string)
		if !ok {
			continue
		}
		var c model.TFCandle
		if json.Unmarshal([]byte(data), &c) == nil && !c.Forming {
			cs = append(cs, c)
		}
	}
	return computeATR(cs)
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
