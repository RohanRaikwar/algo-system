// Package strategy provides the strategy engine for running trading strategies.
//
// A Strategy receives market data (candles, ticks) and emits trading signals (BUY/SELL/EXIT).
// The Engine manages strategy lifecycle: registration, data routing, and signal collection.
package strategy

import (
	"context"
	"log"
	"sort"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/model"
)

// Signal represents a trading signal emitted by a strategy.
type Signal struct {
	StrategyName  string       `json:"strategy_name"`
	Action        Action       `json:"action"` // BUY, SELL, EXIT
	Side          PositionSide `json:"side"`   // CALL, PUT, NONE
	ReverseTo     PositionSide `json:"reverse_to,omitempty"`
	Token         string       `json:"token"`
	Exchange      string       `json:"exchange"`
	Qty           int64        `json:"qty"`
	Price         int64        `json:"price"`                  // FNO LTP at signal time (paise)
	EntryFNOPrice int64        `json:"entry_fno_price"`        // FNO option price at entry (paise, set on exit signals)
	MarketState   string       `json:"market_state,omitempty"` // trending/sideways/choppy/range
	Reason        string       `json:"reason"`
}

// Action represents a trading action.
type Action string

const (
	ActionBuy  Action = "BUY"
	ActionSell Action = "SELL"
	ActionExit Action = "EXIT"
)

// PositionSide represents which option leg is active.
type PositionSide string

const (
	SideNone PositionSide = "NONE"
	SideCall PositionSide = "CALL"
	SidePut  PositionSide = "PUT"
)

// tf1m is the 1-minute timeframe value in seconds.
const tf1m = 60

// tf2m is the 2-minute timeframe value in seconds.
const tf2m = 120

// tf3m is the 3-minute timeframe value in seconds.
const tf3m = 180

// Strategy is the interface that all trading strategies must implement.
type Strategy interface {
	// Name returns the unique name of the strategy.
	Name() string

	// OnCandle is called for each new 1-second candle.
	// Return a Signal if the strategy wants to act, or nil to skip.
	OnCandle(candle model.Candle) *Signal

	// OnTick is called for each raw tick (optional, can be a no-op).
	OnTick(tick model.Tick)
}

// Engine manages registered strategies and routes market data to them.
type Engine struct {
	strategies []Strategy
	signalCh   chan Signal
}

// NewEngine creates a new strategy engine.
func NewEngine(signalBufferSize int) *Engine {
	return &Engine{
		signalCh: make(chan Signal, signalBufferSize),
	}
}

// Register adds a strategy to the engine.
func (e *Engine) Register(s Strategy) {
	e.strategies = append(e.strategies, s)
}

// Signals returns the channel of signals emitted by strategies.
func (e *Engine) Signals() <-chan Signal {
	return e.signalCh
}

// Run consumes candles and routes them to all registered strategies.
// Blocks until ctx is cancelled or candleCh is closed.
func (e *Engine) Run(ctx context.Context, candleCh <-chan model.Candle) {
	for {
		select {
		case <-ctx.Done():
			return
		case candle, ok := <-candleCh:
			if !ok {
				return
			}
			for _, s := range e.strategies {
				if sig := s.OnCandle(candle); sig != nil {
					select {
					case e.signalCh <- *sig:
					default:
						// signal channel full, drop
					}
				}
			}
		}
	}
}

// ────────────────────────────────────────────────────────────────────
// TFStrategy — multi-timeframe strategy interface
// ────────────────────────────────────────────────────────────────────

// TFStrategy evaluates on TFCandle events for multi-timeframe strategies.
// Unlike Strategy (which uses 1s candles), TFStrategy receives fully-built
// TFCandles and performs its own TF routing internally.
// OnTick provides live tick data for real-time stoploss checking.
type TFStrategy interface {
	// Name returns the unique name of the strategy.
	Name() string

	// OnTFCandle is called for each closed TF candle.
	// Return a Signal if the strategy wants to act, or nil to skip.
	OnTFCandle(candle model.TFCandle) *Signal

	// OnTick is called for each raw tick (2-4x/sec) for live stoploss checking.
	// Return a Signal if the stoploss was hit, or nil to skip.
	OnTick(tick model.Tick) *Signal
}

// TFEngine manages registered TF strategies and routes TFCandles to them.
// It enforces a hard gate: forming candles are never forwarded to strategies.
type TFEngine struct {
	strategies []TFStrategy
	signalCh   chan Signal
	dropped    atomic.Int64

	// OnExitDropped is called (optional) when an exit could not be
	// delivered within exitSendTimeout — a position may be left open.
	OnExitDropped func(Signal)
}

// exitSendTimeout bounds how long an exit waits for room in a full signal
// channel. Exits close real positions, so they wait instead of dropping;
// the bound keeps a stalled consumer from wedging the caller forever.
var exitSendTimeout = 5 * time.Second

// emit delivers a signal. Entries are dropped when the channel is full —
// a missed entry is a missed trade. Exits wait up to exitSendTimeout — a
// missed exit leaves a position open.
func (e *TFEngine) emit(sig Signal) {
	select {
	case e.signalCh <- sig:
		return
	default:
	}
	if sig.Action == ActionBuy {
		e.dropped.Add(1)
		log.Printf("[tfengine] ⚠️ signal channel full — dropped entry %s %s %s", sig.StrategyName, sig.Action, sig.Side)
		return
	}
	timer := time.NewTimer(exitSendTimeout)
	defer timer.Stop()
	select {
	case e.signalCh <- sig:
	case <-timer.C:
		e.dropped.Add(1)
		log.Printf("[tfengine] 🚨 signal channel full for %s — DROPPED EXIT %s %s %s, position may still be open",
			exitSendTimeout, sig.StrategyName, sig.Action, sig.Side)
		if e.OnExitDropped != nil {
			e.OnExitDropped(sig)
		}
	}
}

// DroppedSignals returns how many signals were dropped because the signal
// channel was full.
func (e *TFEngine) DroppedSignals() int64 { return e.dropped.Load() }

// NewTFEngine creates a new TF-aware strategy engine.
func NewTFEngine(signalBufferSize int) *TFEngine {
	return &TFEngine{
		signalCh: make(chan Signal, signalBufferSize),
	}
}

// Register adds a TF strategy to the engine.
func (e *TFEngine) Register(s TFStrategy) {
	e.strategies = append(e.strategies, s)
}

// Signals returns the channel of signals emitted by strategies.
func (e *TFEngine) Signals() <-chan Signal {
	return e.signalCh
}

// SendSignal injects a signal into the engine's signal channel.
// Used by indicator-driven strategies that evaluate outside of Run().
// Entries drop if the channel is full; exits wait (see emit).
func (e *TFEngine) SendSignal(sig Signal) {
	e.emit(sig)
}

func (e *TFEngine) Run(ctx context.Context, tfCandleCh <-chan model.TFCandle) {
	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-tfCandleCh:
			if !ok {
				return
			}
			// Hard gate: never evaluate forming candles
			if tfc.Forming {
				continue
			}

			var batch []Signal
			for _, s := range e.strategies {
				if sig := s.OnTFCandle(tfc); sig != nil {
					batch = append(batch, *sig)
				}
			}

			if len(batch) > 0 {
				sortSignals(batch)
				for _, sig := range batch {
					e.emit(sig)
				}
			}
		}
	}
}

func (e *TFEngine) RunTicks(ctx context.Context, tickCh <-chan model.Tick) {
	for {
		select {
		case <-ctx.Done():
			return
		case tick, ok := <-tickCh:
			if !ok {
				return
			}

			var batch []Signal
			for _, s := range e.strategies {
				if sig := s.OnTick(tick); sig != nil {
					batch = append(batch, *sig)
				}
			}

			if len(batch) > 0 {
				sortSignals(batch)
				for _, sig := range batch {
					e.emit(sig)
				}
			}
		}
	}
}

// sortSignals sorts a batch of simultaneous signals.
// Rule 1: Exits always come before Entries.
// Rule 2: Strategy Priority (5M3M > SL2 > 10PTS > RANGE > FNO > SL).
func sortSignals(batch []Signal) {
	strategyPriority := func(name string) int {
		switch name {
		case "NIFTY50_FNO_5M3M":
			return 1
		case "NIFTY50_FNO_SL2":
			return 2
		case "NIFTY50_10PTS":
			return 3
		case "NIFTY50_RANGE":
			return 4
		case "NIFTY50_FNO":
			return 5
		case "NIFTY50_FNO_SL":
			return 6
		default:
			return 7
		}
	}

	sort.SliceStable(batch, func(i, j int) bool {
		// Rule 1: Exits first
		isExitI := batch[i].Action == ActionExit
		isExitJ := batch[j].Action == ActionExit
		if isExitI && !isExitJ {
			return true
		}
		if !isExitI && isExitJ {
			return false
		}

		// Rule 2: Strategy Tier
		return strategyPriority(batch[i].StrategyName) < strategyPriority(batch[j].StrategyName)
	})
}
