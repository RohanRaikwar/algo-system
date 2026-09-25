package strategy

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// slTestConfig returns DefaultNifty50FnOSLConfig with IndexToken cleared
// so test candles are not filtered out.
func slTestConfig() Nifty50FnOSLConfig {
	cfg := DefaultNifty50FnOSLConfig()
	cfg.IndexToken = "" // accept any token in tests
	return cfg
}



// ── CALL Entry ──

func TestNifty50FnOSL_CallEntry_BothCrossAbove(t *testing.T) {
	cfg := slTestConfig()
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Sharp rise → CALL entry
	risePrices := []int64{10500, 11000, 11500, 12000, 12500, 13000, 13500, 14000}
	var sig *Signal
	for i, p := range risePrices {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig = strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			break
		}
		sig = nil
	}

	if sig == nil {
		t.Fatal("expected CALL entry signal, got nil")
	}
	if sig.StrategyName != "NIFTY50_FNO_SL" {
		t.Errorf("expected NIFTY50_FNO_SL, got %s", sig.StrategyName)
	}
	if sig.Side != SideCall {
		t.Errorf("expected CALL side, got %s", sig.Side)
	}
}

// ── PUT Entry ──

func TestNifty50FnOSL_PutEntry_BothCrossBelow(t *testing.T) {
	cfg := slTestConfig()
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	dropPrices := []int64{9500, 9000, 8500, 8000, 7500, 7000, 6500, 6000}
	var sig *Signal
	for i, p := range dropPrices {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig = strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SidePut {
			break
		}
		sig = nil
	}

	if sig == nil {
		t.Fatal("expected PUT entry signal, got nil")
	}
	if sig.Side != SidePut {
		t.Errorf("expected PUT side, got %s", sig.Side)
	}
}

// ── CALL Exit: EMA6 crosses below EMA9 ──

func TestNifty50FnOSL_CallExit_EMA6BelowEMA9(t *testing.T) {
	cfg := slTestConfig()
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000+int64(i+1)*500, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			break
		}
	}

	st := strat.getOrCreate("NSE:NIFTY")
	if st.Side != SideCall {
		t.Fatal("expected to be in CALL position for exit test")
	}

	var exitSig *Signal
	for i := 0; i < 15; i++ {
		ts := base.Add(time.Duration((35+i)*60) * time.Second)
		exitSig = strat.OnTFCandle(make1mTFC("NIFTY", 15000-int64(i)*500, ts))
		if exitSig != nil && exitSig.Action == ActionExit {
			break
		}
		exitSig = nil
	}

	if exitSig == nil {
		t.Fatal("expected CALL exit signal, got nil")
	}
	if exitSig.Action != ActionExit {
		t.Errorf("expected EXIT action, got %s", exitSig.Action)
	}
}

// ── Resistance filter blocks CALL entry ──

func TestNifty50FnOSL_ResistanceBlocksCallEntry(t *testing.T) {
	cfg := slTestConfig()
	cfg.ResistanceLevels = []float64{14000} // resistance at 14000
	cfg.ResistanceDistancePct = 1.0         // 1% zone
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Rise toward resistance level — entries near 14000 should be blocked
	risePrices := []int64{10500, 11000, 11500, 12000, 12500, 13000, 13500, 13900}
	var sig *Signal
	for i, p := range risePrices {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig = strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			// Check if entry is near resistance — if close is within 1% of 14000, it should have been blocked
			closeF := float64(p)
			if isNearResistance(closeF, cfg.ResistanceLevels, cfg.ResistanceDistancePct) {
				t.Fatalf("CALL entry should have been blocked near resistance, close=%d", p)
			}
		}
	}
	// It's acceptable if no signal fires at all (the entry was blocked by resistance)
}

// ── Support filter blocks PUT entry ──

func TestNifty50FnOSL_SupportBlocksPutEntry(t *testing.T) {
	cfg := slTestConfig()
	cfg.SupportLevels = []float64{6000} // support at 6000
	cfg.SupportDistancePct = 1.0        // 1% zone
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	dropPrices := []int64{9500, 9000, 8500, 8000, 7500, 7000, 6500, 6050}
	var sig *Signal
	for i, p := range dropPrices {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig = strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SidePut {
			closeF := float64(p)
			if isNearSupport(closeF, cfg.SupportLevels, cfg.SupportDistancePct) {
				t.Fatalf("PUT entry should have been blocked near support, close=%d", p)
			}
		}
	}
}

// ── Index Hard SL triggers EXIT ──

func TestNifty50FnOSL_IndexHardSL_CallExit(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexHardSLPct = 1.0 // 1% hard SL on index
	cfg.FNOHardSLPct = 0     // disable FNO SL for this test
	cfg.FNOTrailSLPct = 0
	cfg.IndexTrailSLPct = 0
	cfg.IndexToken = "NSE:NIFTY"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	// Manually set up a CALL position
	st := strat.getOrCreate("NSE:NIFTY")
	st.Side = SideCall
	st.IndexEntryPrice = 10000
	st.IndexBestPrice = 10000

	// Tick drops 1.5% → should trigger Hard SL
	tick := model.Tick{
		Token:    "NIFTY",
		Exchange: "NSE",
		Price:    9850, // 1.5% drop
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected INDEX HARD SL exit signal, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT action, got %s", sig.Action)
	}
	if sig.Side != SideCall {
		t.Errorf("expected CALL side, got %s", sig.Side)
	}
}

func TestNifty50FnOSL_IndexHardSL_PutExit(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexHardSLPct = 1.0
	cfg.FNOHardSLPct = 0
	cfg.FNOTrailSLPct = 0
	cfg.IndexTrailSLPct = 0
	cfg.IndexToken = "NSE:NIFTY"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	st := strat.getOrCreate("NSE:NIFTY")
	st.Side = SidePut
	st.IndexEntryPrice = 10000
	st.IndexBestPrice = 10000

	// Tick rises 1.5% → should trigger Hard SL for PUT
	tick := model.Tick{
		Token:    "NIFTY",
		Exchange: "NSE",
		Price:    10150,
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected INDEX HARD SL PUT exit signal, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT, got %s", sig.Action)
	}
}

// ── FNO Hard SL triggers EXIT ──

func TestNifty50FnOSL_FNOHardSL_CallExit(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexHardSLPct = 0 // disable index SL
	cfg.IndexTrailSLPct = 0
	cfg.FNOHardSLPct = 2.0 // 2% hard SL on FNO premium
	cfg.FNOTrailSLPct = 0
	cfg.IndexToken = "NSE:NIFTY"
	cfg.FNOCallToken = "NFO:NIFTYCE"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	st := strat.getOrCreate("NSE:NIFTY")
	st.Side = SideCall
	st.IndexEntryPrice = 10000
	st.IndexBestPrice = 10000
	st.FNOEntryPrice = 50000 // ₹500 premium (in paise)
	st.FNOBestPrice = 50000

	// FNO premium drops 3% → should trigger Hard SL
	tick := model.Tick{
		Token:    "NIFTYCE",
		Exchange: "NFO",
		Price:    48500, // 3% drop from 50000
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected FNO HARD SL CALL exit signal, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT, got %s", sig.Action)
	}
	if sig.Side != SideCall {
		t.Errorf("expected CALL, got %s", sig.Side)
	}
}

func TestNifty50FnOSL_FNOHardSL_PutExit(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexHardSLPct = 0
	cfg.IndexTrailSLPct = 0
	cfg.FNOHardSLPct = 2.0
	cfg.FNOTrailSLPct = 0
	cfg.IndexToken = "NSE:NIFTY"
	cfg.FNOPutToken = "NFO:NIFTYPE"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	st := strat.getOrCreate("NSE:NIFTY")
	st.Side = SidePut
	st.IndexEntryPrice = 10000
	st.IndexBestPrice = 10000
	st.FNOEntryPrice = 50000
	st.FNOBestPrice = 50000

	// FNO premium drops 3% → should trigger Hard SL for PUT (since we are buying options, drop = loss)
	tick := model.Tick{
		Token:    "NIFTYPE",
		Exchange: "NFO",
		Price:    48500,
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected FNO HARD SL PUT exit signal, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT, got %s", sig.Action)
	}
}

// ── Index Trail SL triggers EXIT ──

func TestNifty50FnOSL_IndexTrailSL_CallExit(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexHardSLPct = 0
	cfg.FNOHardSLPct = 0
	cfg.FNOTrailSLPct = 0
	cfg.IndexTrailSLPct = 0.50    // 0.50% trail SL
	cfg.IndexTrailStartPct = 0.10 // start trailing after 0.10% profit
	cfg.IndexToken = "NSE:NIFTY"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	st := strat.getOrCreate("NSE:NIFTY")
	st.Side = SideCall
	st.IndexEntryPrice = 10000
	st.IndexBestPrice = 10200 // already in profit

	// Tick drops 0.6% from best → should trigger trail SL
	tick := model.Tick{
		Token:    "NIFTY",
		Exchange: "NSE",
		Price:    10138, // drop of ~0.61% from best 10200
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected INDEX TRAIL SL CALL exit, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT, got %s", sig.Action)
	}
}

// ── FNO Trail SL triggers EXIT ──

func TestNifty50FnOSL_FNOTrailSL_CallExit(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexHardSLPct = 0
	cfg.IndexTrailSLPct = 0
	cfg.FNOHardSLPct = 0
	cfg.FNOTrailSLPct = 2.0    // 2% trail
	cfg.FNOTrailStartPct = 1.0 // start after 1% profit
	cfg.IndexToken = "NSE:NIFTY"
	cfg.FNOCallToken = "NFO:NIFTYCE"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	st := strat.getOrCreate("NSE:NIFTY")
	st.Side = SideCall
	st.IndexEntryPrice = 10000
	st.IndexBestPrice = 10000
	st.FNOEntryPrice = 50000
	st.FNOBestPrice = 52000 // 4% up from entry

	// FNO drops 2.5% from best → should trigger trail SL
	tick := model.Tick{
		Token:    "NIFTYCE",
		Exchange: "NFO",
		Price:    50700, // 2.5% drop from 52000
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected FNO TRAIL SL CALL exit, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT, got %s", sig.Action)
	}
}

func TestNifty50FnOSL_ResetPosition_NonSLDoesNotConsumeCrossover(t *testing.T) {
	cfg := slTestConfig()
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	st := strat.getOrCreate("NSE:NIFTY")

	// Seed some buffer so consume index math is meaningful.
	st.pushBuffer(nifty50FnOSLBufferEntry{EMA6: 99, EMA9: 100})
	st.pushBuffer(nifty50FnOSLBufferEntry{EMA6: 101, EMA9: 100})
	st.pushBuffer(nifty50FnOSLBufferEntry{EMA6: 102, EMA9: 100})
	st.CrossConsumedAtBufIdx = -1

	// Emulate a normal (non-SL) exit path: SLHitSide should be NONE.
	st.Side = SideCall
	st.SLHitSide = SideNone
	strat.resetPositionSL(st)

	if st.CrossConsumedAtBufIdx != -1 {
		t.Fatalf("non-SL reset consumed crossover unexpectedly: idx=%d", st.CrossConsumedAtBufIdx)
	}
}

func TestNifty50FnOSL_ResetPosition_SLConsumesCrossover(t *testing.T) {
	cfg := slTestConfig()
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	st := strat.getOrCreate("NSE:NIFTY")

	// Seed buffer and emulate SL exit.
	st.pushBuffer(nifty50FnOSLBufferEntry{EMA6: 99, EMA9: 100})
	st.pushBuffer(nifty50FnOSLBufferEntry{EMA6: 101, EMA9: 100})
	st.CrossConsumedAtBufIdx = -1
	st.Side = SidePut
	st.SLHitSide = SidePut

	expected := (st.BufferIdx - 1 + len(st.Buffer)) % len(st.Buffer)
	strat.resetPositionSL(st)

	if st.CrossConsumedAtBufIdx != expected {
		t.Fatalf("SL reset should consume crossover at %d, got %d", expected, st.CrossConsumedAtBufIdx)
	}
}

// ── OnTick returns nil for unknown ticks ──

func TestNifty50FnOSL_OnTickReturnsNil_UnknownToken(t *testing.T) {
	strat := NewNifty50FnOSL(1)
	tick := model.Tick{Token: "BANKNIFTY", Exchange: "NSE", Price: 50000}
	sig := strat.OnTick(tick)
	if sig != nil {
		t.Fatal("OnTick should return nil for unknown tokens")
	}
}

// ── Snapshot/Restore ──

func TestNifty50FnOSL_SnapshotRestore(t *testing.T) {
	cfg := slTestConfig()
	strat := NewNifty50FnOSLWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	data, err := strat.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	strat2 := NewNifty50FnOSLWithConfig(1, cfg)
	if err := strat2.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	strat2.mu.Lock()
	_, ok := strat2.instruments["NSE:NIFTY"]
	strat2.mu.Unlock()
	if !ok {
		t.Fatal("expected NSE:NIFTY in restored instruments")
	}
}

func TestNifty50FnOSL_CurrentFNOPosition(t *testing.T) {
	cfg := slTestConfig()
	cfg.IndexToken = "NSE:99926000"
	cfg.FNOCallToken = "NFO:62582"
	strat := NewNifty50FnOSLWithConfig(1, cfg)

	strat.mu.Lock()
	st := strat.getOrCreate(cfg.IndexToken)
	st.Side = SideCall
	st.FNOEntryPrice = 50100
	st.FNOBestPrice = 51950
	strat.mu.Unlock()

	pos := strat.CurrentFNOPosition()
	if pos == nil {
		t.Fatal("expected live FNO position")
	}
	if pos.Side != SideCall {
		t.Fatalf("side = %s, want %s", pos.Side, SideCall)
	}
	if pos.Token != "NFO:62582" {
		t.Fatalf("token = %q, want %q", pos.Token, "NFO:62582")
	}
	if pos.EntryPrice != 50100 || pos.BestPrice != 51950 {
		t.Fatalf("unexpected prices: entry=%d best=%d", pos.EntryPrice, pos.BestPrice)
	}
}

// ── Resistance / Support helper tests ──

func TestIsNearResistance(t *testing.T) {
	levels := []float64{23500, 24000}

	// Close at 23480 → 23500 is 0.085% away → within 0.10% → blocked
	if !isNearResistance(23480, levels, 0.10) {
		t.Error("expected 23480 to be near resistance 23500 within 0.10%")
	}

	// Close at 23000 → far from any level
	if isNearResistance(23000, levels, 0.10) {
		t.Error("expected 23000 to NOT be near resistance")
	}

	// Nil levels → never blocked
	if isNearResistance(23500, nil, 0.10) {
		t.Error("expected nil levels to never block")
	}
}

func TestIsNearSupport(t *testing.T) {
	levels := []float64{22000, 22500}

	// Close at 22010 → 22000 is ~0.045% away → within 0.10% → blocked
	if !isNearSupport(22010, levels, 0.10) {
		t.Error("expected 22010 to be near support 22000 within 0.10%")
	}

	// Close at 23000 → far from any level
	if isNearSupport(23000, levels, 0.10) {
		t.Error("expected 23000 to NOT be near support")
	}
}
