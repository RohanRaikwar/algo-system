package strategy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// rangeTestConfig judges entries on 1m bars so single injected candles
// drive the logic; 5m entry gating has its own tests (range5m_test.go).
func rangeTestConfig() Nifty50RangeConfig {
	cfg := DefaultNifty50RangeConfig()
	cfg.IndexToken = ""
	cfg.EntryTFMinutes = 1
	cfg.MeanReversionEnabled = true // default off; the S1 logic is still tested
	return cfg
}

func tfc(ts time.Time, b ohlcv) model.TFCandle {
	return model.TFCandle{Token: "NIFTY", Exchange: "NSE", TF: tf1m, TS: ts,
		Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
}

// rangeFeed generates the 23,900-24,100 triangle wave as directional bars
// (open = previous close) and remembers where it is.
type rangeFeed struct {
	idx  int
	prev int64
	day  time.Time
}

func newRangeFeed() *rangeFeed {
	return &rangeFeed{prev: pts(23900), day: time.Date(2026, 1, 5, 0, 0, 0, 0, ist)}
}

func (f *rangeFeed) next() int64 {
	p := rangePrice(f.idx, pts(23900), pts(24100), 120)
	f.idx++
	return p
}

func (f *rangeFeed) bar(p int64) ohlcv {
	b := ohlcv{Open: f.prev, Close: p, High: maxInt64(f.prev, p) + 200, Low: minInt64(f.prev, p) - 200}
	f.prev = p
	return b
}

// warmDays returns n days of wave candles.
func (f *rangeFeed) warmDays(n int) []model.TFCandle {
	var out []model.TFCandle
	for d := 0; d < n; d++ {
		for _, ts := range sessionMinutes(f.day) {
			out = append(out, tfc(ts, f.bar(f.next())))
		}
		f.day = f.day.AddDate(0, 0, 1)
	}
	return out
}

// newWarmRange returns a strategy warmed with 3 range days and the feed
// positioned at the start of day 4.
func newWarmRange(t *testing.T, cfg Nifty50RangeConfig) (*Nifty50Range, *rangeFeed) {
	t.Helper()
	s := NewNifty50RangeWithConfig(1, cfg)
	f := newRangeFeed()
	if n := s.Warmup(f.warmDays(3)); n == 0 {
		t.Fatal("warmup consumed nothing")
	}
	return s, f
}

// runUntil feeds wave bars of day 4 until minute m (exclusive, IST), returning
// any signals that fired.
func runUntil(s *Nifty50Range, f *rangeFeed, h, m int) []Signal {
	var sigs []Signal
	for _, ts := range sessionMinutes(f.day) {
		if minutesIST(ts) >= hhmm(h, m) {
			break
		}
		if sig := s.OnTFCandle(tfc(ts, f.bar(f.next()))); sig != nil {
			sigs = append(sigs, *sig)
		}
	}
	return sigs
}

func day4(f *rangeFeed, h, m int) time.Time {
	return time.Date(f.day.Year(), f.day.Month(), f.day.Day(), h, m, 0, 0, ist)
}

// hammerAt builds a bullish hammer whose close sits just above support.
func hammerAt(sup int64) ohlcv {
	return ohlcv{Open: sup + 500, High: sup + 1100, Low: sup - 2000, Close: sup + 1000}
}

func shootingStarAt(res int64) ohlcv {
	return ohlcv{Open: res - 500, High: res + 2000, Low: res - 1100, Close: res - 1000}
}

func TestRangeS1CallAtSupport(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	// Day 4 wave reaches the support trough at 10:29.
	if sigs := runUntil(s, f, 10, 29); len(sigs) != 0 {
		t.Fatalf("unexpected signals before support: %+v", sigs)
	}
	rs, _ := s.RangeState("NSE:NIFTY")
	if !rs.InRange {
		t.Fatalf("not in range: %+v", rs)
	}
	sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))
	if sig == nil {
		t.Fatalf("expected CALL entry at support; state=%+v", rs)
	}
	if sig.Action != ActionBuy || sig.Side != SideCall || sig.StrategyName != "NIFTY50_RANGE" {
		t.Fatalf("got %+v", sig)
	}
	if !strings.Contains(sig.Reason, RangeKindMeanReversion) || sig.MarketState != "range" {
		t.Errorf("reason/market state: %q %q", sig.Reason, sig.MarketState)
	}
	st := s.instruments["NSE:NIFTY"]
	after, _ := s.RangeState("NSE:NIFTY") // the 10:29 close also closed a 15m bar (ATR moved)
	if st.StopLevel != after.Support-s.stopBuffer(after) || st.TargetLevel <= rs.Support {
		t.Errorf("stop=%d target=%d support=%d", st.StopLevel, st.TargetLevel, rs.Support)
	}
}

func TestRangeS1NeedsReversalCandle(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	// Plain bearish bar at support: no reversal pattern.
	plain := ohlcv{Open: rs.Support + 1500, High: rs.Support + 1600, Low: rs.Support + 400, Close: rs.Support + 500}
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), plain)); sig != nil {
		t.Fatalf("entry without reversal candle: %+v", sig)
	}
}

func TestRangeS1NeedsLowRSI(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.RSIBuyMax = 0 // nothing passes
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("entry despite RSI filter: %+v", sig)
	}
}

func TestRangeS1NeedsVolumeWhenRequired(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.RequireVolume = true // wave has zero volume
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("entry without volume data while required: %+v", sig)
	}
}

func TestRangeS1NoEntryMidRange(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 0))
	rs, _ := s.RangeState("NSE:NIFTY")
	mid := (rs.Support + rs.Resistance) / 2
	if sig := s.OnTFCandle(tfc(day4(f, 10, 0), hammerAt(mid))); sig != nil {
		t.Fatalf("mid-range entry: %+v", sig)
	}
}

func TestRangeS1NoEntryOutsideWindow(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.MeanRevWindows = []timeWindow{{From: hhmm(14, 0), To: hhmm(15, 0)}}
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("entry outside window: %+v", sig)
	}
}

func TestRangeS1PutAtResistanceAndExitHasSide(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	// Day 4: wave peaks at 09:30 and again at 11:30 (outside 11:30 window end) —
	// use the 09:30 peak shifted into the window by starting the window earlier.
	s.cfg.MeanRevWindows = []timeWindow{{From: hhmm(9, 20), To: hhmm(11, 30)}}
	quiet(t, runUntil(s, f, 9, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	sig := s.OnTFCandle(tfc(day4(f, 9, 29), shootingStarAt(rs.Resistance)))
	if sig == nil || sig.Side != SidePut || sig.Action != ActionBuy {
		t.Fatalf("expected PUT entry, got %+v (rsi5=%.1f)", sig, rs.RSI5)
	}
	// Price rallies through the stop.
	stop := s.instruments["NSE:NIFTY"].StopLevel
	ex := s.OnTFCandle(tfc(day4(f, 9, 30), ohlcv{Open: rs.Resistance, High: stop + 500, Low: rs.Resistance, Close: stop + 100}))
	if ex == nil || ex.Action != ActionExit || ex.Side != SidePut || !strings.Contains(ex.Reason, "STOP") {
		t.Fatalf("expected PUT stop exit with side, got %+v", ex)
	}
}

func TestRangeTimeExitAndForceExitCarrySide(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig == nil {
		t.Fatal("setup entry failed")
	}
	st := s.instruments["NSE:NIFTY"]
	st.StopLevel, st.TargetLevel, st.TrailArm = 1, 1<<40, 1<<40 // only time can exit
	p := rs.Support + 1000
	ex := s.OnTFCandle(tfc(day4(f, 14, 59), ohlcv{Open: p, High: p, Low: p, Close: p}))
	if ex == nil || ex.Side != SideCall || !strings.Contains(ex.Reason, "TIME EXIT") {
		t.Fatalf("expected 15:00 time exit, got %+v", ex)
	}
	if !strings.Contains(ex.Reason, "15:00") {
		t.Fatalf("exit time in reason: %q", ex.Reason)
	}

	st.Side = SidePut
	sigs := s.ForceExitAll("EOD")
	if len(sigs) != 1 || sigs[0].Side != SidePut || sigs[0].Action != ActionExit {
		t.Fatalf("ForceExitAll = %+v", sigs)
	}
}

func TestRangePremiumHardSL(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	s.SetFNOTokens("NFO:CE1", "NFO:PE1")
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))

	if sig := s.OnTick(model.Tick{Token: "CE1", Exchange: "NFO", Price: 5000}); sig != nil {
		t.Fatal("premium check must wait for the entry price")
	}
	s.SetFNOEntryPrice(10000)
	if pos := s.CurrentFNOPosition(); pos == nil || pos.Token != "NFO:CE1" || pos.EntryPrice != 10000 {
		t.Fatalf("CurrentFNOPosition = %+v", pos)
	}
	if sig := s.OnTick(model.Tick{Token: "CE1", Exchange: "NFO", Price: 8500}); sig != nil {
		t.Fatalf("15%% drop must not hit 20%% SL: %+v", sig)
	}
	sig := s.OnTick(model.Tick{Token: "CE1", Exchange: "NFO", Price: 7900})
	if sig == nil || sig.Action != ActionExit || sig.Side != SideCall || sig.Token != "NIFTY" {
		t.Fatalf("expected hard SL exit, got %+v", sig)
	}
}

// breakoutBar is a strong bullish bar closing c.
func breakoutBar(open, c int64) ohlcv {
	return ohlcv{Open: open, High: c + 200, Low: open - 200, Close: c}
}

func TestRangeS2BreakoutWithFollowThrough(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.MeanReversionEnabled = false
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 11, 20)) // rising leg toward the 11:30 peak
	rs, _ := s.RangeState("NSE:NIFTY")
	if !rs.InRange {
		t.Fatalf("not in range: %+v", rs)
	}
	edge := rs.Resistance
	if sig := s.OnTFCandle(tfc(day4(f, 11, 20), breakoutBar(edge-2000, edge+1500))); sig != nil {
		t.Fatalf("must wait for follow-through, got %+v", sig)
	}
	sig := s.OnTFCandle(tfc(day4(f, 11, 21), breakoutBar(edge+1500, edge+1800)))
	if sig == nil || sig.Action != ActionBuy || sig.Side != SideCall || !strings.Contains(sig.Reason, "BREAKOUT") {
		t.Fatalf("expected breakout CALL, got %+v", sig)
	}
	st := s.instruments["NSE:NIFTY"]
	wantStop := edge - maxInt64(cfg.BreakoutStopPts, s.stopBuffer(st.ctx.State()))
	if st.StopLevel != wantStop || st.TargetLevel != edge+rs.Width*cfg.BreakoutTargetPct/100 {
		t.Errorf("stop=%d target=%d edge=%d width=%d", st.StopLevel, st.TargetLevel, edge, rs.Width)
	}
}

func TestRangeS2FalseBreakout(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.MeanReversionEnabled = false
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 11, 20))
	rs, _ := s.RangeState("NSE:NIFTY")
	edge := rs.Resistance
	s.OnTFCandle(tfc(day4(f, 11, 20), breakoutBar(edge-2000, edge+2500)))
	back := ohlcv{Open: edge + 2500, High: edge + 2600, Low: edge - 1500, Close: edge - 1000}
	if sig := s.OnTFCandle(tfc(day4(f, 11, 21), back)); sig != nil {
		t.Fatalf("false breakout traded: %+v", sig)
	}
}

func TestRangeS2BreakoutNeedsVolumeWhenAvailable(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.MeanReversionEnabled = false
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 11, 0))
	// 20 bars of volume 100, then the breakout bar with only 120 (< 150%).
	for i := 0; i < 20; i++ {
		ts := day4(f, 11, i)
		b := f.bar(f.next())
		b.Volume = 100
		s.OnTFCandle(tfc(ts, b))
	}
	rs, _ := s.RangeState("NSE:NIFTY")
	edge := rs.Resistance
	b := breakoutBar(edge-2000, edge+2500)
	b.Volume = 120
	s.OnTFCandle(tfc(day4(f, 11, 20), b))
	if s.instruments["NSE:NIFTY"].PendingBreak != SideNone {
		t.Fatal("low-volume breakout must not arm")
	}
}

func TestRangeS2ReversesOpenS1(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig == nil || sig.Side != SideCall {
		t.Fatal("setup CALL failed")
	}
	st := s.instruments["NSE:NIFTY"]
	st.StopLevel = 1 // keep the CALL open through the breakdown
	edge := rs.Support
	down1 := ohlcv{Open: edge + 2000, High: edge + 2200, Low: edge - 1700, Close: edge - 1500}
	if sig := s.OnTFCandle(tfc(day4(f, 10, 30), down1)); sig != nil {
		t.Fatalf("first breakdown bar should only arm: %+v", sig)
	}
	down2 := ohlcv{Open: edge - 1500, High: edge - 1300, Low: edge - 2000, Close: edge - 1800}
	sig := s.OnTFCandle(tfc(day4(f, 10, 31), down2))
	if sig == nil || sig.Action != ActionExit || sig.Side != SideCall || sig.ReverseTo != SidePut {
		t.Fatalf("expected CALL exit reversing to PUT, got %+v", sig)
	}
	if !strings.Contains(sig.Reason, "close=") {
		t.Error("reverse reason must carry close= for expandReverseSignals")
	}
	if st.Side != SidePut || st.Kind != RangeKindBreakout {
		t.Errorf("state after reverse: side=%s kind=%s", st.Side, st.Kind)
	}
}

func TestRangeMaxTradesPerDay(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.MaxTradesPerDay = 0
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("trade cap ignored: %+v", sig)
	}
}

func TestRangeSnapshotRoundTrip(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))
	s.SetFNOEntryPrice(12345)

	data, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := NewNifty50RangeWithConfig(1, rangeTestConfig())
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	a, b := s.instruments["NSE:NIFTY"], r.instruments["NSE:NIFTY"]
	if b == nil || a.Side != b.Side || a.StopLevel != b.StopLevel || a.TargetLevel != b.TargetLevel ||
		a.FNOEntryPrice != b.FNOEntryPrice || a.TradesToday != b.TradesToday {
		t.Fatalf("restored mismatch:\n%+v\n%+v", a, b)
	}
	ra, rb := a.ctx.State(), b.ctx.State()
	if ra.Support != rb.Support || ra.Resistance != rb.Resistance || ra.InRange != rb.InRange {
		t.Errorf("context mismatch:\n%+v\n%+v", ra, rb)
	}
}

func TestRangeRestoreDropsV1Snapshot(t *testing.T) {
	v1, _ := json.Marshal(map[string]any{"version": 1, "strategy": "NIFTY50_RANGE",
		"instruments": []map[string]any{{"key": "NSE:NIFTY", "side": "CALL", "entry_level": 1.5}}})
	r := NewNifty50RangeWithConfig(1, rangeTestConfig())
	if err := r.Restore(v1); err != nil {
		t.Fatal(err)
	}
	if len(r.instruments) != 0 {
		t.Fatal("v1 snapshot must be dropped")
	}
}

// quiet fails the test if the lead-in wave produced any signal, so a
// negative test can't pass because an earlier bar already opened a position.
func quiet(t *testing.T, sigs []Signal) {
	t.Helper()
	if len(sigs) != 0 {
		t.Fatalf("lead-in produced signals: %+v", sigs)
	}
}

func TestRangeATMStrike(t *testing.T) {
	cases := []struct{ close, want int64 }{
		{pts(23980), 24000}, {pts(23970), 23950}, {pts(23975), 24000}, {pts(24000), 24000},
	}
	for _, c := range cases {
		if got := atmStrike(c.close, 50); got != c.want {
			t.Errorf("atmStrike(%d) = %d, want %d", c.close, got, c.want)
		}
	}
}

func TestRangeEntryCarriesStrikeByRangeClass(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))
	if sig == nil {
		t.Fatal("no entry")
	}
	atm := atmStrike(hammerAt(rs.Support).Close, 50)
	// The synthetic ~204-pt range is WIDE → 1 strike OTM for a CALL.
	if rs.Class != RangeWide || sig.Strike != atm+50 {
		t.Fatalf("class=%s strike=%d, want %d", rs.Class, sig.Strike, atm+50)
	}

	cfg := rangeTestConfig()
	cfg.OTMSteps = map[RangeClass]int{}
	s2, f2 := newWarmRange(t, cfg)
	quiet(t, runUntil(s2, f2, 10, 29))
	rs2, _ := s2.RangeState("NSE:NIFTY")
	sig2 := s2.OnTFCandle(tfc(day4(f2, 10, 29), hammerAt(rs2.Support)))
	if sig2 == nil || sig2.Strike != atmStrike(hammerAt(rs2.Support).Close, 50) {
		t.Fatalf("ATM entry strike = %+v", sig2)
	}
}

func TestRangePutOTMIsBelowATM(t *testing.T) {
	if got := entryStrike(SidePut, pts(24080), 50, 1); got != 24050 {
		t.Fatalf("PUT 1 OTM from 24080 = %d, want 24050", got)
	}
	if got := entryStrike(SideCall, pts(24080), 50, 1); got != 24150 {
		t.Fatalf("CALL 1 OTM from 24080 = %d, want 24150", got)
	}
}

func TestRangePremiumStopUsesPositionToken(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	s.SetFNOTokens("NFO:ATMCE", "NFO:ATMPE")
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))
	s.SetPositionToken("NFO:OTM1CE")
	s.SetFNOEntryPrice(10000)
	if sig := s.OnTick(model.Tick{Token: "ATMCE", Exchange: "NFO", Price: 1}); sig != nil {
		t.Fatal("ATM ticks must not drive a position held in another strike")
	}
	if pos := s.CurrentFNOPosition(); pos == nil || pos.Token != "NFO:OTM1CE" {
		t.Fatalf("position token = %+v", pos)
	}
	sig := s.OnTick(model.Tick{Token: "OTM1CE", Exchange: "NFO", Price: 7000})
	if sig == nil || sig.Action != ActionExit || sig.Side != SideCall {
		t.Fatalf("expected premium SL on the held strike, got %+v", sig)
	}
}

func TestRangeCancelEntryRestoresTradeBudget(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))
	st := s.instruments["NSE:NIFTY"]
	if st.TradesToday != 1 {
		t.Fatalf("trades = %d", st.TradesToday)
	}
	s.CancelEntry(SideCall, "no contract")
	if st.Side != SideNone || st.TradesToday != 0 || st.CooldownLeft != 0 {
		t.Fatalf("after cancel: side=%s trades=%d cooldown=%d", st.Side, st.TradesToday, st.CooldownLeft)
	}
	s.CancelEntry(SidePut, "wrong side is a no-op")
}

func TestRangeNearExpiryForcesATM(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	// Day 4 is 2026-01-08; expiry 2026-01-09 → 1 DTE → ATM even in a WIDE range.
	s.SetExpiry(time.Date(2026, 1, 9, 0, 0, 0, 0, ist))
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	bar := hammerAt(rs.Support)
	sig := s.OnTFCandle(tfc(day4(f, 10, 29), bar))
	if sig == nil || rs.Class != RangeWide || sig.Strike != atmStrike(bar.Close, 50) {
		t.Fatalf("near-expiry strike = %+v (class %s)", sig, rs.Class)
	}

	s2, f2 := newWarmRange(t, rangeTestConfig())
	s2.SetExpiry(time.Date(2026, 1, 13, 0, 0, 0, 0, ist)) // 5 DTE → rule applies (1 OTM)
	quiet(t, runUntil(s2, f2, 10, 29))
	rs2, _ := s2.RangeState("NSE:NIFTY")
	bar2 := hammerAt(rs2.Support)
	if sig2 := s2.OnTFCandle(tfc(day4(f2, 10, 29), bar2)); sig2 == nil || sig2.Strike != atmStrike(bar2.Close, 50)+50 {
		t.Fatalf("5 DTE strike = %+v", sig2)
	}
}

func TestRangeRewardRiskFilter(t *testing.T) {
	cfg := rangeTestConfig()
	cfg.MinRewardRiskPct = 10000 // 100:1 — nothing passes
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("entry despite reward:risk filter: %+v", sig)
	}
	if st := s.instruments["NSE:NIFTY"]; st.Side != SideNone || st.TradesToday != 0 {
		t.Fatalf("refused entry left state: side=%s trades=%d", st.Side, st.TradesToday)
	}
}

func TestRangeEntryCarriesTargetMove(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	bar := hammerAt(rs.Support)
	sig := s.OnTFCandle(tfc(day4(f, 10, 29), bar))
	if sig == nil {
		t.Fatal("no entry")
	}
	st := s.instruments["NSE:NIFTY"]
	if sig.TargetMove != st.TargetLevel-bar.Close || sig.TargetMove <= 0 {
		t.Fatalf("TargetMove = %d, want %d", sig.TargetMove, st.TargetLevel-bar.Close)
	}
}

func TestRangeMeanReversionOffByDefault(t *testing.T) {
	if DefaultNifty50RangeConfig().MeanReversionEnabled {
		t.Fatal("mean reversion must default to off")
	}
	cfg := rangeTestConfig()
	cfg.MeanReversionEnabled = false
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("mean-reversion entry while disabled: %+v", sig)
	}
}

func TestRangeExitsBeforeClosingAuction(t *testing.T) {
	cfg := DefaultNifty50RangeConfig()
	if cfg.TimeExitMin != hhmm(15, 0) {
		t.Fatalf("time exit %d, want 15:00", cfg.TimeExitMin)
	}
	for _, w := range append(cfg.BreakoutWindows, cfg.MeanRevWindows...) {
		if w.To > hhmm(14, 45) {
			t.Fatalf("entry window ends %d, after 14:45", w.To)
		}
	}
	if DefaultNifty50RangeICConfig().TimeExitMin != hhmm(15, 0) {
		t.Fatal("condor must exit by 15:00")
	}
}

func TestRangeForceExitStaleKeepsTodaysPosition(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig == nil {
		t.Fatal("setup entry failed")
	}
	today := f.day.Format("2006-01-02")
	if sigs := s.ForceExitStale(today, "STALE"); len(sigs) != 0 {
		t.Fatalf("same-day position must survive a restart, got %+v", sigs)
	}
	sigs := s.ForceExitStale(f.day.AddDate(0, 0, 1).Format("2006-01-02"), "STALE")
	if len(sigs) != 1 || sigs[0].Side != SideCall || sigs[0].Action != ActionExit {
		t.Fatalf("previous-day position must exit, got %+v", sigs)
	}
}

func TestRangeExitRequested(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	quiet(t, runUntil(s, f, 10, 29))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sig := s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support))); sig == nil {
		t.Fatal("no entry")
	}
	st := s.instruments["NSE:NIFTY"]
	st.FNOToken = "NFO:111"
	if s.ProtectArmed(SideCall) {
		t.Fatal("fresh entry must not be armed")
	}
	st.TrailArmed = true
	if !s.ProtectArmed(SideCall) {
		t.Fatal("armed trail not reported")
	}
	if sig := s.ExitRequested(SideCall, "222", "THETA"); sig != nil || st.Side != SideCall {
		t.Fatalf("other contract closed the position: %+v", sig)
	}
	if sig := s.ExitRequested(SidePut, "", "THETA"); sig != nil {
		t.Fatalf("wrong side closed: %+v", sig)
	}
	sig := s.ExitRequested(SideCall, "111", "THETA CALL flat 60m")
	if sig == nil || sig.Action != ActionExit || sig.Side != SideCall || sig.Reason != "THETA CALL flat 60m" {
		t.Fatalf("exit = %+v", sig)
	}
	if st.Side != SideNone || st.CooldownLeft == 0 {
		t.Fatalf("position not reset: side=%s cooldown=%d", st.Side, st.CooldownLeft)
	}
	if sig := s.ExitRequested(SideCall, "", "again"); sig != nil {
		t.Fatalf("flat strategy exited twice: %+v", sig)
	}
}
