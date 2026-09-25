package strategy

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func optTestConfig() Nifty5010PtsConfig {
	cfg := DefaultNifty5010PtsConfig()
	cfg.IndexToken = "" // accept any token in tests
	cfg.NameOverride = "NIFTY50_10PTS"
	cfg.SkipFirstMinutes = 0
	cfg.SkipLastMinutes = 0
	cfg.TrendEnabled = false
	cfg.SidewaysEnabled = false
	cfg.MomentumBypassPct = 0
	cfg.MinEMA6EMA9DiffPct = 0
	// Disable pullback and big move filters for tests
	cfg.BigMoveEnabled = false
	cfg.PullbackEnabled = false
	cfg.ConfirmationCandleEnabled = false
	return cfg
}


func TestNifty5010Pts_CallEntry_BothCrossAbove(t *testing.T) {
	cfg := optTestConfig()
	strat := NewNifty5010PtsWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up (need >34 candles for SMA34)
	for i := 0; i < 45; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Sharp rise → CALL entry
	risePrices := []int64{10500, 11000, 11500, 12000, 12500, 13000, 13500, 14000}
	var sig *Signal
	for i, p := range risePrices {
		ts := base.Add(time.Duration((45+i)*60) * time.Second)
		sig = strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			break
		}
		sig = nil
	}

	if sig == nil {
		t.Fatal("expected CALL entry signal, got nil")
	}
	if sig.StrategyName != "NIFTY50_10PTS" { // Using the override specified in OptimizedNifty5010PtsConfig
		t.Errorf("expected NIFTY50_10PTS, got %s", sig.StrategyName)
	}
	if sig.Side != SideCall {
		t.Errorf("expected CALL side, got %s", sig.Side)
	}
}

func TestNifty5010Pts_PutEntry_BothCrossBelow(t *testing.T) {
	cfg := optTestConfig()
	strat := NewNifty5010PtsWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 45; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	dropPrices := []int64{9500, 9000, 8500, 8000, 7500, 7000, 6500, 6000}
	var sig *Signal
	for i, p := range dropPrices {
		ts := base.Add(time.Duration((45+i)*60) * time.Second)
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

func TestNifty5010Pts_IndexHardSL_CallExit(t *testing.T) {
	cfg := optTestConfig()
	cfg.IndexHardSLPct = 1.0 // 1% hard SL on index
	cfg.FNOHardSLPct = 0     // disable FNO SL for this test
	cfg.IndexTrailSLPct = 0
	cfg.IndexToken = "NSE:NIFTY"
	strat := NewNifty5010PtsWithConfig(1, cfg)

	// Manually set up a CALL position
	st := strat.getOrCreate10Pts("NSE:NIFTY")
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

func TestNifty5010Pts_TargetProfit_CallExit(t *testing.T) {
	cfg := optTestConfig()
	cfg.FNOTargetProfitPct = 1.5 // 1.5% target (was 1500 paise fixed)
	cfg.FNOHardSLPct = 0         // disable FNO hard SL so only target fires
	cfg.FNOTrailSLPct = 0        // disable FNO trail SL
	cfg.IndexHardSLPct = 0       // disable index SL
	cfg.IndexTrailSLPct = 0
	cfg.IndexToken = "NSE:NIFTY"
	cfg.FNOCallToken = "NFO:NIFTYCE"
	strat := NewNifty5010PtsWithConfig(1, cfg)

	strat.mu.Lock()
	st := strat.getOrCreate10Pts("NSE:NIFTY")
	st.Side = SideCall
	st.IndexEntryPrice = 1000000
	st.IndexBestPrice = 1000000
	st.FNOEntryPrice = 50000  // FNO option entry price (500.00 pts)
	st.FNOBestPrice = 50000
	st.ActiveFNOTargetProfitPct = 1.5 // Set active target
	strat.mu.Unlock()

	// FNO tick that gains >= 1.5% over FNO entry
	// 50000 * 1.015 = 50750
	tick := model.Tick{
		Token:    "NIFTYCE",
		Exchange: "NFO",
		Price:    50750, // 1.5% gain
	}
	sig := strat.OnTick(tick)
	if sig == nil {
		t.Fatal("expected TARGET PROFIT exit signal, got nil")
	}
	if sig.Action != ActionExit {
		t.Errorf("expected EXIT action, got %s", sig.Action)
	}
}

func TestNifty5010Pts_TargetExit_DoesNotReenterOnStaleCrossover(t *testing.T) {
	cfg := optTestConfig()
	cfg.IndexToken = "NSE:NIFTY"
	cfg.FNOCallToken = "NFO:NIFTYCE"
	cfg.FNOTargetProfitPct = 1.0 // 1% target (keep small so test exits quickly)
	cfg.FNOHardSLPct = 0
	cfg.FNOTrailSLPct = 0
	cfg.IndexHardSLPct = 0
	cfg.IndexTrailSLPct = 0

	strat := NewNifty5010PtsWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up EMAs.
	for i := 0; i < 45; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Create one bullish crossover and enter CALL.
	risePrices := []int64{10500, 11000, 11500, 12000, 12500, 13000, 13500, 14000}
	entryOffset := -1
	for i, p := range risePrices {
		offset := 45 + i
		ts := base.Add(time.Duration(offset) * time.Minute)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			entryOffset = offset
			break
		}
	}
	if entryOffset == -1 {
		t.Fatal("expected CALL entry before target-exit check")
	}

	// Arm FNO premium and hit target (1% gain).
	strat.SetFNOEntryPrice(50000)
	exitSig := strat.OnTick(model.Tick{
		Token:    "NIFTYCE",
		Exchange: "NFO",
		Price:    50500, // +500 paise = 1% gain
	})
	if exitSig == nil || exitSig.Action != ActionExit {
		t.Fatal("expected TARGET exit signal")
	}

	// Keep EMA6 > EMA9 (no fresh crossover). Strategy must NOT re-enter from stale crossover.
	for j := 1; j <= 3; j++ {
		ts := base.Add(time.Duration(entryOffset+j) * time.Minute)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 14000+int64(j*100), ts))
		if sig != nil && sig.Action == ActionBuy {
			t.Fatalf("unexpected re-entry on stale crossover at step %d: side=%s reason=%s", j, sig.Side, sig.Reason)
		}
	}
}

func TestNifty5010Pts_TargetExit_ConsumesSameCrossoverSignal(t *testing.T) {
	cfg := optTestConfig()
	cfg.IndexToken = "NSE:NIFTY"
	cfg.FNOCallToken = "NFO:NIFTYCE"
	cfg.FNOTargetProfitPct = 1.0 // 1% target
	cfg.FNOHardSLPct = 0
	cfg.FNOTrailSLPct = 0
	cfg.IndexHardSLPct = 0
	cfg.IndexTrailSLPct = 0

	strat := NewNifty5010PtsWithConfig(1, cfg)
	st := strat.getOrCreate10Pts(cfg.IndexToken)

	// Seed a simple state: one bullish crossover happened and EMA6 remains above EMA9.
	st.pushBuffer(nifty5010PtsBufferEntry{EMA6: 99, EMA9: 100})
	st.pushBuffer(nifty5010PtsBufferEntry{EMA6: 101, EMA9: 100})
	st.pushBuffer(nifty5010PtsBufferEntry{EMA6: 102, EMA9: 100})
	if !st.ema6CrossedAboveEMA9() {
		t.Fatal("precondition failed: expected bullish crossover signal")
	}

	st.Side = SideCall
	st.FNOEntryPrice = 50000
	st.FNOBestPrice = 50000
	st.ActiveFNOTargetProfitPct = 1.0
	// Emulate normal runtime after at least one completed trade cycle.
	// (resetPosition10Pts already normalized this to SideNone.)
	st.SLHitSide = SideNone

	exitSig := strat.OnTick(model.Tick{
		Token:    "NIFTYCE",
		Exchange: "NFO",
		Price:    50500, // 1% target hit
	})
	if exitSig == nil || exitSig.Action != ActionExit {
		t.Fatal("expected target exit signal")
	}

	// Next candle still bullish, but it is the same crossover regime.
	// A stale signal must not remain active after target exit.
	st.pushBuffer(nifty5010PtsBufferEntry{EMA6: 103, EMA9: 100})
	if st.ema6CrossedAboveEMA9() {
		t.Fatal("stale bullish crossover remained active after target exit")
	}
}

func TestNifty5010Pts_CurrentFNOPosition(t *testing.T) {
	cfg := optTestConfig()
	cfg.IndexToken = "NSE:99926000"
	cfg.FNOCallToken = "NFO:12345"
	strat := NewNifty5010PtsWithConfig(1, cfg)

	strat.mu.Lock()
	st := strat.getOrCreate10Pts(cfg.IndexToken)
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
	if pos.Token != "NFO:12345" {
		t.Fatalf("token = %q, want %q", pos.Token, "NFO:12345")
	}
	if pos.EntryPrice != 50100 || pos.BestPrice != 51950 {
		t.Fatalf("unexpected prices: entry=%d best=%d", pos.EntryPrice, pos.BestPrice)
	}
}
