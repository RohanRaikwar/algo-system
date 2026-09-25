# NIFTY50_FNO EMA6/SMA21 Reversal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the approved NIFTY50_FNO redesign: EMA6/SMA21-only entries, SL-driven exits (5/3/2), and deterministic opposite-side reversal.

**Architecture:** Keep `Nifty50FnO` as the decision source, but remove EMA9 from decision gates. Emit explicit reverse exits (`EXIT` + `ReverseTo`) from strategy, then expand them in `stratengine` into ordered `EXIT -> BUY` processing while synchronizing strategy state for the synthetic BUY. Keep EMA9 fields/state/snapshots for compatibility.

**Tech Stack:** Go 1.x, existing strategy engine (`TFEngine`), stratengine signal loop, Go testing (`go test`).

---

## File Structure Map

- Modify: `backend/internal/strategy/nifty50_fno_state.go`
- Responsibility: update defaults to FNO SL `5/3/2` while preserving existing compatibility fields.

- Modify: `backend/internal/strategy/engine.go`
- Responsibility: extend `Signal` with optional reverse metadata (`ReverseTo`).

- Modify: `backend/internal/strategy/nifty50_fno.go`
- Responsibility: EMA6/SMA21-only entry logic, disable EMA9/candle exits, emit reverse exits, add synthetic-entry state sync API, normalize reason strings.

- Modify: `backend/internal/stratengine/service.go`
- Responsibility: expand reverse exits into ordered paired signals and apply synthetic strategy-state entry before processing paired BUY.

- Modify: `backend/internal/strategy/nifty50_fno_test.go`
- Responsibility: unit tests for new defaults, entry criteria, disabled candle exits, reverse exits, synthetic entry sync, and reason format.

- Modify: `backend/internal/stratengine/service_test.go`
- Responsibility: tests for reverse signal expansion ordering and reverse BUY construction.

---

### Task 1: Lock FNO SL Defaults to 5/3/2

**Files:**
- Modify: `backend/internal/strategy/nifty50_fno_test.go`
- Modify: `backend/internal/strategy/nifty50_fno_state.go`
- Test: `backend/internal/strategy/nifty50_fno_test.go`

- [ ] **Step 1: Write the failing default-config test**

```go
func TestDefaultNifty50FnOConfig_FNOSLDefaults(t *testing.T) {
	cfg := DefaultNifty50FnOConfig()

	if cfg.FNOHardSLPct != 5.0 {
		t.Fatalf("FNOHardSLPct = %.2f, want 5.0", cfg.FNOHardSLPct)
	}
	if cfg.FNOTrailSLPct != 3.0 {
		t.Fatalf("FNOTrailSLPct = %.2f, want 3.0", cfg.FNOTrailSLPct)
	}
	if cfg.FNOTrailStartPct != 2.0 {
		t.Fatalf("FNOTrailStartPct = %.2f, want 2.0", cfg.FNOTrailStartPct)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run TestDefaultNifty50FnOConfig_FNOSLDefaults -count=1
```

Expected: FAIL with current values (`7.0`, `5.0`, `5.0`).

- [ ] **Step 3: Update the defaults in config**

```go
// FNO dual SL — active
FNOHardSLPct:     5.0,
FNOTrailSLPct:    3.0,
FNOTrailStartPct: 2.0,
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run TestDefaultNifty50FnOConfig_FNOSLDefaults -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/agile/Desktop/trading-systemv1
git add backend/internal/strategy/nifty50_fno_state.go backend/internal/strategy/nifty50_fno_test.go
git commit -m "fix(strategy): set NIFTY50_FNO FNO SL defaults to 5-3-2"
```

---

### Task 2: Enforce EMA6/SMA21-Only Entry Decisions

**Files:**
- Modify: `backend/internal/strategy/nifty50_fno_test.go`
- Modify: `backend/internal/strategy/nifty50_fno.go`
- Test: `backend/internal/strategy/nifty50_fno_test.go`

- [ ] **Step 1: Write failing tests proving EMA9 is ignored for entry**

```go
func TestNifty50FnO_CallEntry_IgnoresEMA9Position(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.CallSetupActive = true

	candleTS := time.Date(2026, 1, 2, 10, 0, 0, 0, testIST)
	curr := nifty50FnOBufferEntry{
		Time:  candleTS,
		Open:  10000,
		High:  10200,
		Low:   9980,
		Close: 10150,
		EMA6:  10100,
		EMA9:  9900,  // below MA21 on purpose
		MA21:  10000,
	}

	sig := strat.evaluateEntryFnO(
		make1mTFC("NIFTY", 10150, candleTS),
		st,
		curr,
		candleTS,
		true,  // ema6CrossedAboveMA21
		false, // ema9CrossedAboveMA21
		false,
		false,
		false,
		false,
		false, // bothAboveMA21 false on purpose
		false,
		false,
	)

	if sig == nil || sig.Action != ActionBuy || sig.Side != SideCall {
		t.Fatalf("expected BUY CALL signal, got %+v", sig)
	}
}

func TestNifty50FnO_PutEntry_IgnoresEMA9Position(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.PutSetupActive = true

	candleTS := time.Date(2026, 1, 2, 10, 1, 0, 0, testIST)
	curr := nifty50FnOBufferEntry{
		Time:  candleTS,
		Open:  10000,
		High:  10020,
		Low:   9800,
		Close: 9850,
		EMA6:  9900,
		EMA9:  10100, // above MA21 on purpose
		MA21:  10000,
	}

	sig := strat.evaluateEntryFnO(
		make1mTFC("NIFTY", 9850, candleTS),
		st,
		curr,
		candleTS,
		false,
		false,
		true,  // ema6CrossedBelowMA21
		false, // ema9CrossedBelowMA21
		false,
		false,
		false,
		false,
		false,
	)

	if sig == nil || sig.Action != ActionBuy || sig.Side != SidePut {
		t.Fatalf("expected BUY PUT signal, got %+v", sig)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run 'TestNifty50FnO_(CallEntry_IgnoresEMA9Position|PutEntry_IgnoresEMA9Position)$' -count=1
```

Expected: FAIL because current logic still requires `bothAboveMA21` / `bothBelowMA21`.

- [ ] **Step 3: Update entry logic to depend only on EMA6/SMA21**

```go
// inside evaluateEntryFnO
ema6AboveMA21 := curr.EMA6 > curr.MA21
ema6BelowMA21 := curr.EMA6 < curr.MA21

// queue during cooldown
if st.InCooldown {
	if st.PutSetupActive && ema6BelowMA21 && ema6CrossedBelowMA21 {
		st.PendingEntry = SidePut
	}
	if st.CallSetupActive && ema6AboveMA21 && ema6CrossedAboveMA21 {
		st.PendingEntry = SideCall
	}
	return nil
}

// pending execution
if justExitedCooldown && st.PendingEntry != SideNone {
	pending := st.PendingEntry
	st.PendingEntry = SideNone
	if pending == SidePut && st.PutSetupActive && ema6BelowMA21 && vwapOkForPut {
		// emit BUY PUT
	}
	if pending == SideCall && st.CallSetupActive && ema6AboveMA21 && vwapOkForCall {
		// emit BUY CALL
	}
}

// direct entries (no EMA9 crossover re-entry)
if st.CallSetupActive && ema6AboveMA21 && vwapOkForCall {
	if ema6CrossedAboveMA21 {
		// emit BUY CALL
	}
}
if st.PutSetupActive && ema6BelowMA21 && vwapOkForPut {
	if ema6CrossedBelowMA21 {
		// emit BUY PUT
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run 'TestNifty50FnO_(CallEntry_IgnoresEMA9Position|PutEntry_IgnoresEMA9Position)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/agile/Desktop/trading-systemv1
git add backend/internal/strategy/nifty50_fno.go backend/internal/strategy/nifty50_fno_test.go
git commit -m "ref(strategy): switch NIFTY50_FNO entry rules to EMA6-SMA21 only"
```

---

### Task 3: Remove Candle-Based Exits and Emit Reverse Exit Signals

**Files:**
- Modify: `backend/internal/strategy/engine.go`
- Modify: `backend/internal/strategy/nifty50_fno.go`
- Modify: `backend/internal/strategy/nifty50_fno_test.go`
- Test: `backend/internal/strategy/nifty50_fno_test.go`

- [ ] **Step 1: Add failing tests for disabled EMA9 exits and reverse exits**

```go
func TestNifty50FnO_NoExitOnEMA6EMA9CrossOnly(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.Side = SideCall
	st.EntryPrice = 12000

	candleTS := time.Date(2026, 1, 2, 11, 0, 0, 0, testIST)
	prev := nifty50FnOBufferEntry{EMA6: 12100, EMA9: 12000, MA21: 11800}
	curr := nifty50FnOBufferEntry{EMA6: 11900, EMA9: 12050, MA21: 11850, Close: 11950}

	sig := strat.evaluateExitFnO(
		make1mTFC("NIFTY", 11950, candleTS),
		st,
		prev,
		curr,
		true,  // ema6CrossedBelowEMA9
		false, // ema6CrossedAboveEMA9
		false, // ema6CrossedBelowMA21
		false, // ema6CrossedAboveMA21
	)
	if sig != nil {
		t.Fatalf("expected nil exit when only EMA6/EMA9 crosses, got %+v", sig)
	}
}

func TestNifty50FnO_ReverseExit_CallToPut(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.Side = SideCall
	st.EntryPrice = 12000

	candleTS := time.Date(2026, 1, 2, 11, 1, 0, 0, testIST)
	prev := nifty50FnOBufferEntry{EMA6: 12100, MA21: 12000}
	curr := nifty50FnOBufferEntry{EMA6: 11900, MA21: 12000, Close: 11950}

	sig := strat.evaluateExitFnO(
		make1mTFC("NIFTY", 11950, candleTS),
		st,
		prev,
		curr,
		false,
		false,
		true,  // ema6CrossedBelowMA21
		false,
	)

	if sig == nil {
		t.Fatal("expected reverse exit signal, got nil")
	}
	if sig.Action != ActionExit || sig.Side != SideCall || sig.ReverseTo != SidePut {
		t.Fatalf("unexpected reverse signal: %+v", sig)
	}
	if st.Side != SideNone {
		t.Fatalf("expected state reset to NONE, got %s", st.Side)
	}
}

func TestNifty50FnO_ReverseExit_PutToCall(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.Side = SidePut
	st.EntryPrice = 12000

	candleTS := time.Date(2026, 1, 2, 11, 2, 0, 0, testIST)
	prev := nifty50FnOBufferEntry{EMA6: 11900, MA21: 12000}
	curr := nifty50FnOBufferEntry{EMA6: 12100, MA21: 12000, Close: 12080}

	sig := strat.evaluateExitFnO(
		make1mTFC("NIFTY", 12080, candleTS),
		st,
		prev,
		curr,
		false,
		false,
		false,
		true, // ema6CrossedAboveMA21
	)

	if sig == nil {
		t.Fatal("expected reverse exit signal, got nil")
	}
	if sig.Action != ActionExit || sig.Side != SidePut || sig.ReverseTo != SideCall {
		t.Fatalf("unexpected reverse signal: %+v", sig)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run 'TestNifty50FnO_(NoExitOnEMA6EMA9CrossOnly|ReverseExit_CallToPut|ReverseExit_PutToCall)$' -count=1
```

Expected: FAIL (current code exits on EMA6/EMA9 cross and has no `ReverseTo`).

- [ ] **Step 3: Implement reverse metadata and strategy exit logic**

```go
// engine.go
// Signal represents a trading signal emitted by a strategy.
type Signal struct {
	StrategyName  string       `json:"strategy_name"`
	Action        Action       `json:"action"`
	Side          PositionSide `json:"side"`
	ReverseTo     PositionSide `json:"reverse_to,omitempty"`
	Token         string       `json:"token"`
	Exchange      string       `json:"exchange"`
	Qty           int64        `json:"qty"`
	Price         int64        `json:"price"`
	EntryFNOPrice int64        `json:"entry_fno_price"`
	MarketState   string       `json:"market_state,omitempty"`
	Reason        string       `json:"reason"`
}
```

```go
// nifty50_fno.go: OnTFCandle caller
if inMarketHours {
	if sig := s.evaluateExitFnO(
		candle, st, prev, curr,
		ema6CrossedBelowEMA9, ema6CrossedAboveEMA9,
		ema6CrossedBelowMA21, ema6CrossedAboveMA21,
	); sig != nil {
		return sig
	}
}
```

```go
// nifty50_fno.go: evaluateExitFnO signature + body
func (s *Nifty50FnO) evaluateExitFnO(
	candle model.TFCandle,
	st *nifty50FnOState,
	prev, curr nifty50FnOBufferEntry,
	ema6CrossedBelowEMA9, ema6CrossedAboveEMA9 bool,
	ema6CrossedBelowMA21, ema6CrossedAboveMA21 bool,
) *Signal {
	_ = prev
	_ = ema6CrossedBelowEMA9
	_ = ema6CrossedAboveEMA9

	if st.Side == SideNone {
		return nil
	}

	if st.Side == SideCall && ema6CrossedBelowMA21 {
		reason := fmt.Sprintf("EXIT CALL reason=REVERSE_TO_PUT ema6=%.0f sma21=%.0f close=%d", curr.EMA6, curr.MA21, candle.Close)
		s.resetPositionFnO(st)
		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         SideCall,
			ReverseTo:    SidePut,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
		}
	}

	if st.Side == SidePut && ema6CrossedAboveMA21 {
		reason := fmt.Sprintf("EXIT PUT reason=REVERSE_TO_CALL ema6=%.0f sma21=%.0f close=%d", curr.EMA6, curr.MA21, candle.Close)
		s.resetPositionFnO(st)
		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         SidePut,
			ReverseTo:    SideCall,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
		}
	}

	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run 'TestNifty50FnO_(NoExitOnEMA6EMA9CrossOnly|ReverseExit_CallToPut|ReverseExit_PutToCall)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/agile/Desktop/trading-systemv1
git add backend/internal/strategy/engine.go backend/internal/strategy/nifty50_fno.go backend/internal/strategy/nifty50_fno_test.go
git commit -m "feat(strategy): emit reverse exits on opposite EMA6-SMA21 crosses"
```

---

### Task 4: Deterministic Reverse Expansion in Signal Loop + Strategy State Sync

**Files:**
- Modify: `backend/internal/strategy/nifty50_fno.go`
- Modify: `backend/internal/stratengine/service.go`
- Modify: `backend/internal/stratengine/service_test.go`
- Modify: `backend/internal/strategy/nifty50_fno_test.go`
- Test: `backend/internal/stratengine/service_test.go`, `backend/internal/strategy/nifty50_fno_test.go`

- [ ] **Step 1: Write failing tests for reverse expansion ordering and synthetic entry state update**

```go
// service_test.go
func TestExpandReverseSignals_ExitThenBuyOpposite(t *testing.T) {
	exitSig := strategy.Signal{
		StrategyName: "NIFTY50_FNO",
		Action:       strategy.ActionExit,
		Side:         strategy.SideCall,
		ReverseTo:    strategy.SidePut,
		Token:        "99926000",
		Exchange:     "NSE",
		Qty:          1,
		Reason:       "EXIT CALL reason=REVERSE_TO_PUT ema6=11900 sma21=12000 close=11950",
	}

	expanded := expandReverseSignals(exitSig)
	if len(expanded) != 2 {
		t.Fatalf("expanded signals len = %d, want 2", len(expanded))
	}
	if expanded[0].Action != strategy.ActionExit || expanded[0].Side != strategy.SideCall {
		t.Fatalf("expanded[0] = %+v, want EXIT CALL", expanded[0])
	}
	if expanded[1].Action != strategy.ActionBuy || expanded[1].Side != strategy.SidePut {
		t.Fatalf("expanded[1] = %+v, want BUY PUT", expanded[1])
	}
	if expanded[1].ReverseTo != strategy.SideNone {
		t.Fatalf("BUY reverse_to should be NONE, got %s", expanded[1].ReverseTo)
	}
}

func TestExpandReverseSignals_NoReverseKeepsSingleSignal(t *testing.T) {
	sig := strategy.Signal{Action: strategy.ActionExit, Side: strategy.SideCall}
	expanded := expandReverseSignals(sig)
	if len(expanded) != 1 {
		t.Fatalf("expanded signals len = %d, want 1", len(expanded))
	}
}
```

```go
// nifty50_fno_test.go
func TestNifty50FnO_ApplySyntheticEntryUpdatesState(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	strat.ApplySyntheticEntry("NSE", "NIFTY", SidePut, 11950)

	st := strat.getOrCreateFnO("NSE:NIFTY")
	if st.Side != SidePut {
		t.Fatalf("state side = %s, want PUT", st.Side)
	}
	if st.EntryPrice != 11950 || st.IndexEntryPrice != 11950 {
		t.Fatalf("entry/index entry = %d/%d, want 11950/11950", st.EntryPrice, st.IndexEntryPrice)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/stratengine -run 'TestExpandReverseSignals_(ExitThenBuyOpposite|NoReverseKeepsSingleSignal)$' -count=1
go test ./internal/strategy -run TestNifty50FnO_ApplySyntheticEntryUpdatesState -count=1
```

Expected: FAIL (`expandReverseSignals` and `ApplySyntheticEntry` missing).

- [ ] **Step 3: Implement reverse expansion helper and synthetic entry synchronization**

```go
// service.go
func expandReverseSignals(sig strategy.Signal) []strategy.Signal {
	if sig.Action != strategy.ActionExit || sig.ReverseTo == strategy.SideNone {
		return []strategy.Signal{sig}
	}

	closePrice := parseSignalClose(sig.Reason)
	buyReason := fmt.Sprintf("ENTRY %s reason=REVERSE_FROM_%s close=%d", sig.ReverseTo, sig.Side, closePrice)

	buySig := sig
	buySig.Action = strategy.ActionBuy
	buySig.Side = sig.ReverseTo
	buySig.ReverseTo = strategy.SideNone
	buySig.Reason = buyReason
	buySig.EntryFNOPrice = 0
	buySig.Price = 0

	return []strategy.Signal{sig, buySig}
}

func (svc *Service) applySyntheticStrategyEntry(sig strategy.Signal) {
	if sig.Action != strategy.ActionBuy {
		return
	}
	if !strings.Contains(sig.Reason, "REVERSE_FROM_") {
		return
	}
	if svc.nifty50Strategy == nil || sig.StrategyName != svc.nifty50Strategy.Name() {
		return
	}

	indexPrice := parseSignalClose(sig.Reason)
	if indexPrice <= 0 {
		return
	}
	svc.nifty50Strategy.ApplySyntheticEntry(sig.Exchange, sig.Token, sig.Side, indexPrice)
}
```

```go
// service.go inside signalLoop, directly after receiving sig from channel
for _, expandedSig := range expandReverseSignals(sig) {
	sig := expandedSig
	svc.applySyntheticStrategyEntry(sig)

	// existing signal processing body stays the same from here onward
	// (kill-switch, market-hours guard, option automation, journal, notify, execute)
}
```

```go
// nifty50_fno.go
func (s *Nifty50FnO) ApplySyntheticEntry(exchange, token string, side PositionSide, indexPrice int64) {
	if side == SideNone || indexPrice <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.cfg.IndexToken
	if key == "" {
		key = exchange + ":" + token
	}
	st := s.getOrCreateFnO(key)

	st.Side = side
	st.EntryPrice = indexPrice
	st.BestPrice = indexPrice
	st.IndexEntryPrice = indexPrice
	st.IndexBestPrice = indexPrice
	if side == SideCall {
		st.CallSetupActive = true
	} else if side == SidePut {
		st.PutSetupActive = true
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/stratengine -run 'TestExpandReverseSignals_(ExitThenBuyOpposite|NoReverseKeepsSingleSignal)$' -count=1
go test ./internal/strategy -run TestNifty50FnO_ApplySyntheticEntryUpdatesState -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/agile/Desktop/trading-systemv1
git add backend/internal/stratengine/service.go backend/internal/stratengine/service_test.go backend/internal/strategy/nifty50_fno.go backend/internal/strategy/nifty50_fno_test.go
git commit -m "feat(stratengine): expand reverse exits into ordered exit-buy pairs"
```

---

### Task 5: Normalize Reason Strings and Validate Full Regression

**Files:**
- Modify: `backend/internal/strategy/nifty50_fno.go`
- Modify: `backend/internal/strategy/nifty50_fno_test.go`
- Modify: `backend/internal/stratengine/service.go`
- Test: `backend/internal/strategy/nifty50_fno_test.go`, `backend/internal/stratengine/service_test.go`

- [ ] **Step 1: Write failing tests for reason token format**

```go
func TestNifty50FnO_EntryReason_UsesENTRYPrefix(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.CallSetupActive = true

	ts := time.Date(2026, 1, 2, 12, 0, 0, 0, testIST)
	curr := nifty50FnOBufferEntry{EMA6: 10100, EMA9: 9900, MA21: 10000, Close: 10150}
	sig := strat.evaluateEntryFnO(make1mTFC("NIFTY", 10150, ts), st, curr, ts, true, false, false, false, false, false, false, false, false)
	if sig == nil {
		t.Fatal("expected entry signal")
	}
	if !strings.Contains(sig.Reason, "ENTRY CALL") {
		t.Fatalf("entry reason = %q, want ENTRY CALL token", sig.Reason)
	}
	if !strings.Contains(sig.Reason, "close=") {
		t.Fatalf("entry reason = %q, want close= marker", sig.Reason)
	}
}

func TestNifty50FnO_FNOHardSLReason_UsesExitToken(t *testing.T) {
	cfg := testConfig()
	cfg.FNOHardSLPct = 5.0
	cfg.FNOTrailSLPct = 0
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.Side = SideCall
	st.FNOEntryPrice = 10000
	st.FNOBestPrice = 10000

	sig := strat.checkFNOSLFnO(model.Tick{Exchange: "NFO", Token: "TEST", Price: 9400}, st)
	if sig == nil {
		t.Fatal("expected hard SL exit signal")
	}
	if !strings.Contains(sig.Reason, "reason=FNO_HARD_SL") {
		t.Fatalf("hard SL reason = %q, want reason=FNO_HARD_SL", sig.Reason)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run 'TestNifty50FnO_(EntryReason_UsesENTRYPrefix|FNOHardSLReason_UsesExitToken)$' -count=1
```

Expected: FAIL with old reason formats.

- [ ] **Step 3: Update reason strings and include `reverse_to` in publish payload**

```go
// nifty50_fno.go entry reason examples
reason := fmt.Sprintf("ENTRY CALL ema6=%.0f sma21=%.0f close=%d mode=normal", curr.EMA6, curr.MA21, candle.Close)
reason := fmt.Sprintf("ENTRY PUT ema6=%.0f sma21=%.0f close=%d mode=normal", curr.EMA6, curr.MA21, candle.Close)

// checkFNOSLFnO reason examples
reason := fmt.Sprintf("EXIT %s reason=FNO_TRAIL_SL fno_price=%d fno_best=%d trail=%.2f close=%d", st.Side, tick.Price, st.FNOBestPrice, s.cfg.FNOTrailSLPct, tick.Price)
reason := fmt.Sprintf("EXIT %s reason=FNO_HARD_SL fno_price=%d fno_entry=%d sl=%.2f close=%d", st.Side, tick.Price, st.FNOEntryPrice, s.cfg.FNOHardSLPct, tick.Price)
```

```go
// service.go signal publish payload map
"reverse_to": string(sig.ReverseTo),
```

- [ ] **Step 4: Run strategy and stratengine regression suites**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -count=1
go test ./internal/stratengine -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/agile/Desktop/trading-systemv1
git add backend/internal/strategy/nifty50_fno.go backend/internal/strategy/nifty50_fno_test.go backend/internal/stratengine/service.go backend/internal/stratengine/service_test.go
git commit -m "ref(logging): standardize NIFTY50_FNO entry-exit reason tokens"
```

---

### Task 6: Final End-to-End Verification and Guardrail Checks

**Files:**
- Modify: none required
- Test: `backend/internal/strategy/...`, `backend/internal/stratengine/...`

- [ ] **Step 1: Run focused reverse flow tests**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy -run 'TestNifty50FnO_(ReverseExit_CallToPut|ReverseExit_PutToCall|ApplySyntheticEntryUpdatesState)$' -count=1
go test ./internal/stratengine -run 'TestExpandReverseSignals_(ExitThenBuyOpposite|NoReverseKeepsSingleSignal)$' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full package tests for touched domains**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
go test ./internal/strategy ./internal/stratengine -count=1
```

Expected: PASS with no compile errors.

- [ ] **Step 3: Run static formatting check**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1/backend
gofmt -w internal/strategy/engine.go internal/strategy/nifty50_fno.go internal/strategy/nifty50_fno_state.go internal/strategy/nifty50_fno_test.go internal/stratengine/service.go internal/stratengine/service_test.go
go test ./internal/strategy ./internal/stratengine -count=1
```

Expected: files formatted, tests still PASS.

- [ ] **Step 4: Verify git diff is limited to planned files**

Run:

```bash
cd /home/agile/Desktop/trading-systemv1
git status --short
```

Expected: only planned strategy/stratengine/test files are modified.

- [ ] **Step 5: Commit verification checkpoint**

```bash
cd /home/agile/Desktop/trading-systemv1
git add backend/internal/strategy/engine.go backend/internal/strategy/nifty50_fno.go backend/internal/strategy/nifty50_fno_state.go backend/internal/strategy/nifty50_fno_test.go backend/internal/stratengine/service.go backend/internal/stratengine/service_test.go
git commit -m "test(strategy): verify EMA6-SMA21 reverse flow and SL-only exits"
```

---

## Spec-to-Plan Coverage Check

- EMA6/SMA21-only entry: covered in Task 2.
- No EMA9 decision dependency (but retain compatibility fields): covered in Task 2 + Task 3.
- SL defaults `5/3/2`: covered in Task 1.
- SL-driven exit and no candle exits: covered in Task 3 + Task 5.
- Deterministic opposite reversal: covered in Task 3 + Task 4.
- Logging cleanup with stable tokens: covered in Task 5.
- Regression safety (strategy + stratengine): covered in Task 6.

## Placeholder Scan

- No TODO/TBD markers.
- All tasks include concrete files, concrete code snippets, exact commands, and expected outcomes.

## Type/Signature Consistency Check

- New `Signal.ReverseTo PositionSide` is optional and JSON-compatible.
- `evaluateExitFnO` signature update is reflected in caller and tests.
- `ApplySyntheticEntry(exchange, token, side, indexPrice)` is referenced consistently from `Service`.

