package omsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/oms"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// executor is the slice of orderexec.OrderExecutor the service drives.
type executor interface {
	ExecuteSignal(sig strategy.Signal)
	UpdateLTP(tick model.Tick)
	Snapshot() ([]byte, error)
	Restore(data []byte) error
	GetEntryOrders() map[string]orderexec.OrderRecord
}

// intentClaimer dedupes intents across redeliveries and restarts.
type intentClaimer interface {
	ClaimIntent(intentID string, at time.Time) (bool, error)
}

// Service is the OMS process.
type Service struct {
	cfg     Config
	rdb     *goredis.Client
	bus     *oms.Bus
	oe      executor
	claims  intentClaimer
	journal *oms.Journal
	now     func() time.Time
}

// FillEvent is published on oms.EventStream for every executor fill.
type FillEvent struct {
	Strategy  string    `json:"strategy"`
	Side      string    `json:"side"`
	Direction string    `json:"direction"`
	Token     string    `json:"token"`
	Price     int64     `json:"price_paise"`
	Qty       int64     `json:"qty"`
	Real      bool      `json:"real"`
	TS        time.Time `json:"ts"`
}

// New connects to Redis, opens the journal and builds a paper executor.
func New(cfg Config) (*Service, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("redis %s: %w", cfg.RedisAddr, err)
	}
	j, err := oms.OpenJournal(cfg.JournalPath)
	if err != nil {
		rdb.Close()
		return nil, fmt.Errorf("journal %s: %w", cfg.JournalPath, err)
	}
	ledger, err := oms.NewLedger(j)
	if err != nil {
		j.Close()
		rdb.Close()
		return nil, err
	}

	// Phase 3 shadow: LiveOrders is hard-coded false and no credentials
	// are passed, so this executor can never reach the broker whatever
	// the environment says. stratengine still owns real orders.
	oe := orderexec.NewOrderExecutor(orderexec.Config{
		LiveOrders:            false,
		Qty:                   cfg.Qty,
		FNOExchange:           cfg.FNOExchange,
		LogPrefix:             "[oms]",
		PaperSlippageBps:      cfg.PaperSlippageBps,
		PaperSlippageMinPaise: cfg.PaperSlippageMinPaise,
	})
	oe.SetOrderLedger(ledger)

	s := &Service{cfg: cfg, rdb: rdb, bus: oms.NewBus(rdb), oe: oe, claims: j, journal: j, now: time.Now}
	oe.SetFillListener(s.onFill)
	oe.SetStateListener(s.persist)
	return s, nil
}

// Close releases Redis and the journal.
func (s *Service) Close() {
	s.journal.Close()
	s.rdb.Close()
}

// Run holds the leader lock and consumes intents until ctx ends or the
// lock is lost.
func (s *Service) Run(ctx context.Context) error {
	id := instanceID(s.cfg.Consumer)
	if err := acquireLeader(ctx, s.rdb, s.cfg.LeaderKey, id, s.cfg.LeaderTTL); err != nil {
		return nil // shut down while waiting
	}
	log.Printf("[oms] 👑 leader lock %s acquired (%s) — PAPER/shadow mode", s.cfg.LeaderKey, id)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lost := make(chan struct{})
	go holdLeader(ctx, s.rdb, s.cfg.LeaderKey, id, s.cfg.LeaderTTL, func() { close(lost); cancel() })

	s.restore(ctx)
	go s.tickLoop(ctx)

	err := s.bus.ConsumeIntents(ctx, s.cfg.Group, s.cfg.Consumer, s.handle)
	s.persist()
	select {
	case <-lost:
		return errors.New("leader lock lost")
	default:
	}
	return err
}

// handle runs one intent through the executor.
func (s *Service) handle(in oms.Intent) error {
	var sig strategy.Signal
	if err := json.Unmarshal(in.Payload, &sig); err != nil {
		return fmt.Errorf("decode signal: %w", err)
	}
	isExit := sig.Action != strategy.ActionBuy
	now := s.now()
	if in.Stale(now, s.cfg.EntryTTL, isExit) {
		s.publish("skipped", map[string]any{"intent": in.ID, "reason": "stale", "age_ms": now.Sub(in.CreatedTS).Milliseconds()})
		return fmt.Errorf("stale entry intent (%s old) skipped", now.Sub(in.CreatedTS).Round(time.Millisecond))
	}
	claimed, err := s.claims.ClaimIntent("intent:"+in.ID, now)
	if err != nil {
		// Without a durable claim a redelivery could run twice; an entry
		// can be skipped, an exit cannot.
		if !isExit {
			return fmt.Errorf("claim intent: %w — entry skipped", err)
		}
		log.Printf("[oms] ⚠️  intent %s claim failed (%v) — exit run anyway", in.ID, err)
	} else if !claimed {
		return fmt.Errorf("duplicate intent %s skipped", in.ID)
	}
	s.oe.ExecuteSignal(sig)
	s.persist()
	return nil
}

func (s *Service) onFill(f orderexec.FillReport) {
	s.publish("fill", FillEvent{
		Strategy:  f.Signal.StrategyName,
		Side:      string(f.Signal.Side),
		Direction: f.Direction,
		Token:     f.Signal.FNOToken,
		Price:     f.FillPricePaise,
		Qty:       f.Qty,
		Real:      f.Real,
		TS:        s.now(),
	})
}

func (s *Service) publish(kind string, v any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.bus.PublishEvent(ctx, kind, v); err != nil {
		log.Printf("[oms] ⚠️  publish %s event: %v", kind, err)
	}
}

// persist saves the executor snapshot and the dashboard positions view.
func (s *Service) persist() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if snap, err := s.oe.Snapshot(); err == nil {
		if err := s.rdb.Set(ctx, s.cfg.SnapshotKey, snap, 0).Err(); err != nil {
			log.Printf("[oms] ⚠️  save snapshot: %v", err)
		}
	}
	if pos, err := json.Marshal(s.oe.GetEntryOrders()); err == nil {
		s.rdb.Set(ctx, s.cfg.PositionsKey, pos, 0)
	}
}

func (s *Service) restore(ctx context.Context) {
	data, err := s.rdb.Get(ctx, s.cfg.SnapshotKey).Bytes()
	switch {
	case errors.Is(err, goredis.Nil):
		log.Printf("[oms] no snapshot at %s — starting flat", s.cfg.SnapshotKey)
	case err != nil:
		log.Printf("[oms] ⚠️  read snapshot: %v — starting flat", err)
	default:
		if err := s.oe.Restore(data); err != nil {
			log.Printf("[oms] ⚠️  %v — starting flat", err)
			return
		}
		log.Printf("[oms] restored %d position(s) from %s", len(s.oe.GetEntryOrders()), s.cfg.SnapshotKey)
	}
}

// tickLoop feeds live prices to the executor for paper fills.
func (s *Service) tickLoop(ctx context.Context) {
	ps := s.rdb.PSubscribe(ctx, "pub:tick:*")
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
			var t model.Tick
			if json.Unmarshal([]byte(msg.Payload), &t) == nil {
				s.oe.UpdateLTP(t)
			}
		}
	}
}
