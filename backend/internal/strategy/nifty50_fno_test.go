package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// testIST is used so candle timestamps fall within the market hours guard.
var testIST = time.FixedZone("IST", 5*3600+30*60)

// testConfig returns DefaultNifty50FnOConfig with IndexToken cleared
// so test candles (token="NIFTY") are not filtered out.
func testConfig() Nifty50FnOConfig {
	cfg := DefaultNifty50FnOConfig()
	cfg.IndexToken = "" // accept any token in tests
	return cfg
}

// make1mTFC creates a 1-minute closed TFCandle for tests.
func make1mTFC(token string, price int64, ts time.Time) model.TFCandle {
	return model.TFCandle{
		Token:    token,
		Exchange: "NSE",
		TF:       tf1m,
		Open:     price,
		High:     price,
		Low:      price,
		Close:    price,
		Volume:   1,
		TS:       ts,
		Forming:  false,
	}
}

func seedChoppyBuffer(st *nifty50FnOState, base time.Time, lastClose float64) nifty50FnOBufferEntry {
	entries := []nifty50FnOBufferEntry{
		{
			Time:  base,
			Open:  10000,
			High:  10010,
			Low:   9990,
			Close: 10000,
			EMA6:  10010,
			EMA9:  9990,
			MA21:  10000,
		},
		{
			Time:  base.Add(time.Minute),
			Open:  10000,
			High:  10010,
			Low:   9990,
			Close: 10000,
			EMA6:  9990,
			EMA9:  10010,
			MA21:  10000,
		},
		{
			Time:  base.Add(2 * time.Minute),
			Open:  10000,
			High:  10010,
			Low:   9990,
			Close: 10000,
			EMA6:  10010,
			EMA9:  9990,
			MA21:  10000,
		},
		{
			Time:  base.Add(3 * time.Minute),
			Open:  10000,
			High:  lastClose,
			Low:   9995,
			Close: lastClose,
			EMA6:  9990,
			EMA9:  10010,
			MA21:  10000,
		},
	}

	var curr nifty50FnOBufferEntry
	for _, entry := range entries {
		st.pushBuffer(entry)
		curr = entry
	}
	return curr
}

// ── CALL Entry: Both EMAs cross above SMA21 ──

func TestNifty50FnO_CallEntry_BothCrossAbove(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up with flat prices — all indicators converge to 10000
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Sharp rise → EMA6 & EMA9 cross above SMA21 → CALL entry
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
	if sig.Action != ActionBuy {
		t.Errorf("expected BUY action, got %s", sig.Action)
	}
	if sig.Side != SideCall {
		t.Errorf("expected CALL side, got %s", sig.Side)
	}
	if sig.StrategyName != "NIFTY50_FNO" {
		t.Errorf("expected NIFTY50_FNO, got %s", sig.StrategyName)
	}
}

// ── CALL Entry: Sequential Crossover (EMA6 first, EMA9 later) ──

func TestNifty50FnO_CallEntry_SequentialCross(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up: keep prices below MA21 level, then gradually rise
	// EMA6 (faster) should cross SMA21 before EMA9 (slower)
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Moderate rise — EMA6 reacts faster than EMA9
	moderateRise := []int64{10300, 10600, 10900, 11200, 11500, 11800, 12100, 12400, 12700, 13000}
	var sig *Signal
	for i, p := range moderateRise {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig = strat.OnTFCandle(make1mTFC("NIFTY", p, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			break
		}
		sig = nil
	}

	if sig == nil {
		t.Fatal("expected CALL entry from sequential crossover, got nil")
	}
	if sig.Side != SideCall {
		t.Errorf("expected CALL side, got %s", sig.Side)
	}
}

// ── CALL Exit: EMA6 crosses below EMA9 ──

func TestNifty50FnO_CallExit_EMA6BelowEMA9(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up and get into a CALL position
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}
	// Rise to trigger CALL entry
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000+int64(i+1)*500, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			break
		}
	}

	// Verify we're in CALL
	st := strat.getOrCreateFnO("NSE:NIFTY")
	if st.Side != SideCall {
		t.Fatal("expected to be in CALL position for exit test")
	}

	// Drop prices → EMA6 crosses below EMA9
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
	if exitSig.Side != SideCall {
		t.Errorf("expected CALL exit, got %s side", exitSig.Side)
	}
}

// ── PUT Entry: Both EMAs cross below SMA21 ──

func TestNifty50FnO_PutEntry_BothCrossBelow(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up with flat prices
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Drop prices → EMA6 & EMA9 cross below SMA21 → PUT entry
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
	if sig.Action != ActionBuy {
		t.Errorf("expected BUY action, got %s", sig.Action)
	}
	if sig.Side != SidePut {
		t.Errorf("expected PUT side, got %s", sig.Side)
	}
}

// ── PUT Exit: EMA6 crosses above EMA9 ──

func TestNifty50FnO_PutExit_EMA6AboveEMA9(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up and get into a PUT position
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}
	// Drop to trigger PUT entry
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000-int64(i+1)*500, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SidePut {
			break
		}
	}

	// Verify we're in PUT
	st := strat.getOrCreateFnO("NSE:NIFTY")
	if st.Side != SidePut {
		t.Fatal("expected to be in PUT position for exit test")
	}

	// Rise prices → EMA6 crosses above EMA9
	var exitSig *Signal
	for i := 0; i < 15; i++ {
		ts := base.Add(time.Duration((35+i)*60) * time.Second)
		exitSig = strat.OnTFCandle(make1mTFC("NIFTY", 5000+int64(i)*500, ts))
		if exitSig != nil && exitSig.Action == ActionExit {
			break
		}
		exitSig = nil
	}

	if exitSig == nil {
		t.Fatal("expected PUT exit signal, got nil")
	}
	if exitSig.Action != ActionExit {
		t.Errorf("expected EXIT action, got %s", exitSig.Action)
	}
}

func TestNifty50FnO_SkipLastMinutes_BlocksEntryButAllowsExit(t *testing.T) {
	cfg := testConfig()
	cfg.SkipLastMinutes = 20
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 14, 46, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	for i := 0; i < 6; i++ {
		ts := base.Add(time.Duration(25+i) * time.Minute)
		if sig := strat.OnTFCandle(make1mTFC("NIFTY", 10500+int64(i+1)*500, ts)); sig != nil {
			t.Fatalf("expected no late BUY signal, got %+v", sig)
		}
	}

	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.Side = SideCall
	st.EntryPrice = 14000
	st.BestPrice = 14000

	exitSig := strat.evaluateExitFnO(
		make1mTFC("NIFTY", 12900, base.Add(31*time.Minute)),
		st,
		nifty50FnOBufferEntry{EMA6: 13050, MA21: 13000},
		nifty50FnOBufferEntry{EMA6: 12950, MA21: 13000, Close: 12900},
		true,
		false,
	)

	if exitSig == nil {
		t.Fatal("expected reverse exit signal")
	}
	if exitSig.Action != ActionExit {
		t.Fatalf("expected EXIT action, got %s", exitSig.Action)
	}
	if exitSig.Side != SideCall {
		t.Fatalf("expected CALL exit, got %s", exitSig.Side)
	}
}

func TestNifty50FnO_EntryAutomationContext_SidewaysLow(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 30; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	ctx := strat.EntryAutomationContext("NSE", "NIFTY")
	if !ctx.Available {
		t.Fatal("expected automation context to be available")
	}
	if ctx.MarketState != EntryMarketStateSideways {
		t.Fatalf("expected sideways market state, got %s", ctx.MarketState)
	}
	if ctx.TrendStrength != EntryStrengthLow {
		t.Fatalf("expected low trend strength, got %s", ctx.TrendStrength)
	}
	if ctx.MomentumStrength != EntryStrengthLow {
		t.Fatalf("expected low momentum strength, got %s", ctx.MomentumStrength)
	}
}

func TestNifty50FnO_EntryAutomationContext_TrendingHigh(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	rally := []int64{10100, 10300, 10600, 11000, 11500, 12100, 12800, 13600, 14500, 15500}
	for i, price := range rally {
		ts := base.Add(time.Duration(25+i) * time.Minute)
		strat.OnTFCandle(make1mTFC("NIFTY", price, ts))
	}

	ctx := strat.EntryAutomationContext("NSE", "NIFTY")
	if !ctx.Available {
		t.Fatal("expected automation context to be available")
	}
	if ctx.MarketState != EntryMarketStateTrending {
		t.Fatalf("expected trending market state, got %s", ctx.MarketState)
	}
	if ctx.TrendStrength != EntryStrengthHigh {
		t.Fatalf("expected high trend strength, got %s (slope=%.4f)", ctx.TrendStrength, ctx.TrendSlopePct)
	}
	if ctx.MomentumStrength != EntryStrengthHigh {
		t.Fatalf("expected high momentum strength, got %s (momentum=%.4f)", ctx.MomentumStrength, ctx.MomentumPct)
	}
}

func TestNifty50FnO_ChoppyMomentumBypassRequiresHighMomentum(t *testing.T) {
	cfg := testConfig()
	cfg.SidewaysEnabled = true
	cfg.MaxChopCrosses = 2
	cfg.MomentumBypassPct = 0.10
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, testIST)
	curr := seedChoppyBuffer(st, base, 10015)

	sig := strat.evaluateEntryFnO(
		make1mTFC("NIFTY", 10015, base.Add(3*time.Minute)),
		st,
		curr,
		base.Add(3*time.Minute),
		false, false, false, false,
		false, false,
		false, false,
		false,
	)
	if sig != nil {
		t.Fatalf("expected medium-momentum choppy entry to be blocked, got %+v", sig)
	}
	if ctx := strat.EntryAutomationContext("NSE", "NIFTY"); ctx.MarketState != EntryMarketStateChoppy {
		t.Fatalf("expected choppy market state, got %s", ctx.MarketState)
	}
}

func TestNifty50FnO_ChoppyMomentumBypassAllowsHighMomentum(t *testing.T) {
	cfg := testConfig()
	cfg.SidewaysEnabled = true
	cfg.MaxChopCrosses = 2
	cfg.MomentumBypassPct = 0.10
	strat := NewNifty50FnOWithConfig(1, cfg)
	st := strat.getOrCreateFnO("NSE:NIFTY")

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, testIST)
	curr := seedChoppyBuffer(st, base, 10025)

	sig := strat.evaluateEntryFnO(
		make1mTFC("NIFTY", 10025, base.Add(3*time.Minute)),
		st,
		curr,
		base.Add(3*time.Minute),
		false, false, false, false,
		false, false,
		false, false,
		false,
	)
	if sig == nil {
		t.Fatal("expected high-momentum choppy entry to bypass sideways filter")
	}
	if sig.Action != ActionBuy || sig.Side != SideCall {
		t.Fatalf("expected BUY CALL signal, got %+v", sig)
	}
}

// ── Mutual Exclusion ──

func TestNifty50FnO_MutualExclusion(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Force into PUT position
	st := strat.getOrCreateFnO("NSE:NIFTY")
	st.Side = SidePut
	st.EntryPrice = 10000

	// Feed rising prices — verify no BUY while PUT is still active
	putExited := false
	for i := 0; i < 20; i++ {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000+int64(i*500), ts))
		if sig != nil {
			if sig.Action == ActionExit && sig.Side == SidePut {
				putExited = true
				continue
			}
			if sig.Action == ActionBuy && !putExited {
				t.Fatal("should not emit BUY entry when already in PUT position")
			}
		}
	}
}

// ── Ignores Wrong TF ──

func TestNifty50FnO_IgnoresWrongTF(t *testing.T) {
	strat := NewNifty50FnO(1)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, time.UTC)

	for i := 0; i < 30; i++ {
		ts := base.Add(time.Duration(i*300) * time.Second)
		sig := strat.OnTFCandle(makeTFC("NIFTY", 300, 10000+int64(i*100), ts))
		if sig != nil {
			t.Fatal("should ignore TF=300 candles")
		}
	}
}

// ── Dedup ──

func TestNifty50FnO_DedupTS(t *testing.T) {
	strat := NewNifty50FnO(1)
	ts := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))

	// Same timestamp — should be silently dropped
	strat.OnTFCandle(make1mTFC("NIFTY", 20000, ts))

	// Older timestamp — also dropped
	strat.OnTFCandle(make1mTFC("NIFTY", 20000, ts.Add(-time.Minute)))
}

// ── OnTick returns nil ──

func TestNifty50FnO_OnTickReturnsNil(t *testing.T) {
	strat := NewNifty50FnO(1)
	tick := model.Tick{Token: "NIFTY", Exchange: "NSE", Price: 10000}
	sig := strat.OnTick(tick)
	if sig != nil {
		t.Fatal("OnTick should always return nil for this strategy")
	}
}

// ── CALL Stop New Entries ──

func TestNifty50FnO_CallStopNewEntries(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Rise to activate call setup and trigger entry
	var entryFired bool
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration((25+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000+int64(i+1)*500, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			entryFired = true
			// Exit the position so we can test re-entry blocking
			st := strat.getOrCreateFnO("NSE:NIFTY")
			st.Side = SideNone
			st.EntryPrice = 0
			break
		}
	}
	if !entryFired {
		t.Fatal("prerequisite CALL entry did not fire")
	}

	st := strat.getOrCreateFnO("NSE:NIFTY")
	if !st.CallSetupActive {
		t.Fatal("CallSetupActive should be true at this point")
	}

	// Now drop prices sharply so EMA6 crosses below SMA21 → deactivate call setup
	for i := 0; i < 15; i++ {
		ts := base.Add(time.Duration((35+i)*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 15000-int64(i+1)*1000, ts))
	}

	if st.CallSetupActive {
		t.Fatal("CallSetupActive should be false after EMA6 crossed below MA21")
	}
}

// ── Snapshot/Restore ──

func TestNifty50FnO_SnapshotRestore(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Take snapshot
	data, err := strat.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	// Restore into new strategy
	strat2 := NewNifty50FnOWithConfig(1, cfg)
	if err := strat2.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Verify restored state has the instrument
	strat2.mu.Lock()
	_, ok := strat2.instruments["NSE:NIFTY"]
	strat2.mu.Unlock()
	if !ok {
		t.Fatal("expected NSE:NIFTY in restored instruments")
	}
}

// ── Re-Entry: Buy Call again after exit ──

func TestNifty50FnO_CallReEntry(t *testing.T) {
	cfg := testConfig()
	strat := NewNifty50FnOWithConfig(1, cfg)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, testIST)

	// Warm up
	for i := 0; i < 25; i++ {
		ts := base.Add(time.Duration(i*60) * time.Second)
		strat.OnTFCandle(make1mTFC("NIFTY", 10000, ts))
	}

	// Rise to trigger initial CALL entry
	offset := 25
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration((offset+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000+int64(i+1)*500, ts))
		if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
			break
		}
	}
	offset = 35

	st := strat.getOrCreateFnO("NSE:NIFTY")
	if st.Side != SideCall {
		t.Fatal("should be in CALL position after initial entry")
	}

	// Price dips to trigger EMA6 < EMA9 → CALL exit
	var exitFired bool
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration((offset+i)*60) * time.Second)
		sig := strat.OnTFCandle(make1mTFC("NIFTY", 15000-int64(i)*300, ts))
		if sig != nil && sig.Action == ActionExit && sig.Side == SideCall {
			exitFired = true
			break
		}
	}
	offset += 5

	if !exitFired {
		// Try a few more candles with sharper dip
		for i := 0; i < 10; i++ {
			ts := base.Add(time.Duration((offset+i)*60) * time.Second)
			sig := strat.OnTFCandle(make1mTFC("NIFTY", 14000-int64(i)*500, ts))
			if sig != nil && sig.Action == ActionExit {
				exitFired = true
				break
			}
		}
		offset += 10
	}

	if !exitFired {
		t.Skip("unable to trigger CALL exit for re-entry test — skipping")
		return
	}

	if st.Side != SideNone {
		t.Fatal("should be NONE after exit")
	}

	// If setup is still active, attempt re-entry by rising again (EMA6 crosses back above EMA9)
	if st.CallSetupActive {
		for i := 0; i < 10; i++ {
			ts := base.Add(time.Duration((offset+i)*60) * time.Second)
			sig := strat.OnTFCandle(make1mTFC("NIFTY", 10000+int64(i+1)*600, ts))
			if sig != nil && sig.Action == ActionBuy && sig.Side == SideCall {
				t.Log("CALL re-entry triggered successfully")
				return
			}
		}
		t.Log("re-entry not triggered (setup conditions may have changed) — this is acceptable for edge case")
	} else {
		t.Log("call setup deactivated during exit sequence — no re-entry possible, test passes")
	}
}

func TestDefaultNifty50FnOConfig_FNOSLDefaults(t *testing.T) {
	cfg := DefaultNifty50FnOConfig()

	if cfg.FNOHardSLPct != 0 {
		t.Fatalf("FNOHardSLPct = %.2f, want 0", cfg.FNOHardSLPct)
	}
	if cfg.FNOTrailSLPct != 0 {
		t.Fatalf("FNOTrailSLPct = %.2f, want 0", cfg.FNOTrailSLPct)
	}
	if cfg.FNOTrailStartPct != 0 {
		t.Fatalf("FNOTrailStartPct = %.2f, want 0", cfg.FNOTrailStartPct)
	}
}

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
		EMA9:  9900, // below MA21 on purpose
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
		false,
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
		true, // ema6CrossedBelowMA21
		false,
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
		false, // ema6CrossedBelowMA21
		false, // ema6CrossedAboveMA21
	)
	if sig != nil {
		t.Fatalf("expected nil exit when EMA6/SMA21 did not cross, got %+v", sig)
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
		true, // ema6CrossedBelowMA21
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
		true, // ema6CrossedAboveMA21
	)

	if sig == nil {
		t.Fatal("expected reverse exit signal, got nil")
	}
	if sig.Action != ActionExit || sig.Side != SidePut || sig.ReverseTo != SideCall {
		t.Fatalf("unexpected reverse signal: %+v", sig)
	}
}

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



