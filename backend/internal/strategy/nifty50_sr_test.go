package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// srTest builds a strategy with one flat index state and a hand-made view,
// so each setup is driven by the two 5m bars passed in.
func srTest(t *testing.T) (*Nifty50SR, *nifty50SRState) {
	t.Helper()
	cfg := DefaultNifty50SRConfig()
	cfg.IndexToken = ""
	// The fixture range is only 40 pts wide: a 10-pt / half-ATR stop keeps
	// reward:risk passable so each test exercises its own filter.
	cfg.StopATRPct, cfg.StopMinPts = 50, 1000
	s := NewNifty50SR(65, cfg)
	st := s.getOrCreate("NSE:NIFTY")
	st.TradeDay = "2026-09-28"
	return s, st
}

// srView: levels at 22,810 / 22,850 / 22,900, 15m ATR 20 pts.
func srView(regime SRRegime, vwap int64, rsi float64) SRState {
	return SRState{
		Range:   RangeState{ADX: 18, ADXReady: true, ATR15: pts(20), RSI5: rsi, RSIReady: true},
		Regime:  regime,
		VWAP:    vwap,
		EMAFast: pts(22830), EMASlow: pts(22830), EMAReady: true,
		Levels: []SRLevel{
			{Price: pts(22810), Touches: 3, Source: SRLevelSwing},
			{Price: pts(22850), Touches: 2, Source: SRLevelSwing},
			{Price: pts(22900), Touches: 2, Source: SRLevelPrevDay},
		},
	}
}

var srNow = time.Date(2026, 9, 28, 10, 30, 0, 0, ist)

func srCandle(close int64) model.TFCandle {
	return model.TFCandle{Token: "NIFTY", Exchange: "NSE", TF: tf1m, TS: srNow, Close: close}
}

const srMin = 10*60 + 30

func TestSRFadeCallAtSupport(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeRange, pts(22830), 35)
	prev := ohlcv{Open: pts(22825), High: pts(22826), Low: pts(22812), Close: pts(22814)}
	cur := ohlcv{Open: pts(22814), High: pts(22816), Low: pts(22806), Close: pts(22815)} // hammer on 22,810
	cur.Open, cur.Close = pts(22813), pts(22816)
	cur.High = pts(22817)
	sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil {
		t.Fatalf("no fade entry; rejects=%v", s.rejects)
	}
	if sig.Side != SideCall || st.Kind != SRKindFade {
		t.Fatalf("side=%s kind=%s", sig.Side, st.Kind)
	}
	if st.StopLevel >= pts(22810) {
		t.Errorf("stop %d not below support", st.StopLevel)
	}
	if st.TargetLevel != pts(22850) {
		t.Errorf("target %d, want next level 22850", st.TargetLevel)
	}
	if sig.MarketState != "range" || sig.Strike != 22800 {
		t.Errorf("market_state=%q strike=%d", sig.MarketState, sig.Strike)
	}
}

func TestSRFadePutAtResistance(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeRange, pts(22830), 65)
	ss.Levels = ss.Levels[:2] // no 22,900: support 22,810 is the target
	prev := ohlcv{Open: pts(22838), High: pts(22848), Low: pts(22837), Close: pts(22846)}
	cur := ohlcv{Open: pts(22847), High: pts(22855), Low: pts(22844), Close: pts(22845)} // shooting star
	sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil {
		t.Fatalf("no fade entry; rejects=%v", s.rejects)
	}
	if sig.Side != SidePut || st.StopLevel <= pts(22850) || st.TargetLevel != pts(22810) {
		t.Fatalf("side=%s stop=%d target=%d", sig.Side, st.StopLevel, st.TargetLevel)
	}
}

func TestSRFadeNeedsRejectionCandle(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeRange, pts(22830), 35)
	prev := ohlcv{Open: pts(22825), High: pts(22826), Low: pts(22812), Close: pts(22814)}
	cur := ohlcv{Open: pts(22814), High: pts(22815), Low: pts(22808), Close: pts(22809)} // plain red bar
	cur.Close = pts(22811)
	if sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("entered without a rejection candle: %s", sig.Reason)
	}
	if s.rejects["fade:no_pattern"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func TestSRNeedsThreeConfirmations(t *testing.T) {
	s, st := srTest(t)
	// Price above VWAP, RSI high, EMA falling: only the candle agrees.
	ss := srView(SRRegimeRange, pts(22800), 55)
	ss.EMAFast, ss.EMASlow = pts(22800), pts(22830)
	prev := ohlcv{Open: pts(22825), High: pts(22826), Low: pts(22812), Close: pts(22814)}
	cur := ohlcv{Open: pts(22813), High: pts(22817), Low: pts(22806), Close: pts(22816)}
	if sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("entered on 1 confirmation: %s", sig.Reason)
	}
	if s.rejects["fade:confirmations_1"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func TestSRBreakdownWaitsForRetest(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeMixed, pts(22825), 40)
	ss.EMAFast, ss.EMASlow = pts(22800), pts(22820)

	// Strong red 5m bar closes 8 pts under 22,810 (≥ 25% of ATR 20).
	prev := ohlcv{Open: pts(22818), High: pts(22820), Low: pts(22811), Close: pts(22812)}
	brk := ohlcv{Open: pts(22812), High: pts(22813), Low: pts(22800), Close: pts(22802)}
	if !s.armBreak(st, ss, prev, brk) {
		t.Fatal("break not armed")
	}
	if st.PendingSide != SidePut || st.PendingLevel != pts(22810) {
		t.Fatalf("pending=%s level=%d", st.PendingSide, st.PendingLevel)
	}
	if st.Side != SideNone {
		t.Fatal("entered on the break bar")
	}

	// Retest: wick back into 22,810, close below with a long upper wick.
	ss.Levels = append(ss.Levels, SRLevel{Price: pts(22760), Touches: 2, Source: SRLevelSwing})
	retest := ohlcv{Open: pts(22804), High: pts(22811), Low: pts(22800), Close: pts(22801)}
	sig := s.evaluateRetest(srCandle(retest.Close), st, ss, brk, retest, srMin)
	if sig == nil {
		t.Fatalf("no retest entry; rejects=%v", s.rejects)
	}
	if sig.Side != SidePut || st.Kind != SRKindRetest || st.PendingSide != SideNone {
		t.Fatalf("side=%s kind=%s pending=%s", sig.Side, st.Kind, st.PendingSide)
	}
	if st.StopLevel <= pts(22810) || st.TargetLevel != pts(22760) {
		t.Errorf("stop=%d target=%d", st.StopLevel, st.TargetLevel)
	}
}

func TestSRRetestFailsWhenPriceReclaims(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeMixed, pts(22825), 40)
	st.PendingSide, st.PendingLevel, st.PendingLeft = SidePut, pts(22810), 6
	back := ohlcv{Open: pts(22806), High: pts(22825), Low: pts(22805), Close: pts(22824)}
	if sig := s.evaluateRetest(srCandle(back.Close), st, ss, back, back, srMin); sig != nil {
		t.Fatal("entered a failed break")
	}
	if st.PendingSide != SideNone {
		t.Error("failed break still pending")
	}
}

func TestSRRetestExpires(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeMixed, pts(22825), 40)
	st.PendingSide, st.PendingLevel, st.PendingLeft = SidePut, pts(22810), 1
	away := ohlcv{Open: pts(22790), High: pts(22792), Low: pts(22780), Close: pts(22782)}
	s.evaluateRetest(srCandle(away.Close), st, ss, away, away, srMin)
	if st.PendingSide != SideNone || s.rejects["retest:expired"] != 1 {
		t.Fatalf("pending=%s rejects=%v", st.PendingSide, s.rejects)
	}
}

func TestSRPullbackToVWAPInUptrend(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeTrendUp, pts(22820), 50)
	ss.EMAFast, ss.EMASlow = pts(22828), pts(22815)
	ss.Levels = []SRLevel{{Price: pts(22700), Touches: 2}, {Price: pts(22900), Touches: 2}}
	prev := ohlcv{Open: pts(22832), High: pts(22833), Low: pts(22822), Close: pts(22824)}
	cur := ohlcv{Open: pts(22823), High: pts(22836), Low: pts(22818), Close: pts(22835)}
	sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil {
		t.Fatalf("no pullback entry; rejects=%v", s.rejects)
	}
	if sig.Side != SideCall || st.Kind != SRKindPullback || sig.MarketState != "trending" {
		t.Fatalf("side=%s kind=%s ms=%s", sig.Side, st.Kind, sig.MarketState)
	}
	if st.StopLevel >= pts(22818) {
		t.Errorf("stop %d not below the pullback low", st.StopLevel)
	}
	if want := cur.Close + st.Risk*2; st.TargetLevel != want {
		t.Errorf("target %d, want 2R %d", st.TargetLevel, want)
	}
}

func TestSRPullbackRefusedWhenLevelInTheWay(t *testing.T) {
	s, st := srTest(t)
	ss := srView(SRRegimeTrendUp, pts(22820), 50)
	ss.EMAFast, ss.EMASlow = pts(22828), pts(22815)
	ss.Levels = []SRLevel{{Price: pts(22845), Touches: 3}} // 10 pts above entry
	prev := ohlcv{Open: pts(22832), High: pts(22833), Low: pts(22822), Close: pts(22824)}
	cur := ohlcv{Open: pts(22823), High: pts(22836), Low: pts(22818), Close: pts(22835)}
	if sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("entered into resistance: %s", sig.Reason)
	}
	if s.rejects["pullback:level_in_way"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func fadeSetup() (SRState, ohlcv, ohlcv) {
	ss := srView(SRRegimeRange, pts(22830), 35)
	prev := ohlcv{Open: pts(22825), High: pts(22826), Low: pts(22812), Close: pts(22814)}
	cur := ohlcv{Open: pts(22813), High: pts(22817), Low: pts(22806), Close: pts(22816)}
	return ss, prev, cur
}

func TestSRDailyCaps(t *testing.T) {
	ss, prev, cur := fadeSetup()
	cases := []struct {
		name  string
		set   func(*nifty50SRState)
		min   int
		block string
	}{
		{"trade cap", func(st *nifty50SRState) { st.TradesToday = 3 }, srMin, "trade_cap"},
		{"two losses", func(st *nifty50SRState) { st.ConsecLosses = 2 }, srMin, "consec_losses"},
		{"day loss", func(st *nifty50SRState) { st.DayPnLPts = -pts(60) }, srMin, "day_loss"},
		{"cooldown", func(st *nifty50SRState) { st.CooldownLeft = 3 }, srMin, "cooldown"},
		{"too early", func(*nifty50SRState) {}, hhmm(9, 25), "time_window"},
		{"too late", func(*nifty50SRState) {}, hhmm(14, 45), "time_window"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, st := srTest(t)
			tc.set(st)
			if sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, tc.min); sig != nil {
				t.Fatalf("entry not blocked: %s", sig.Reason)
			}
			if s.rejects[tc.block] != 1 {
				t.Errorf("rejects=%v, want %s", s.rejects, tc.block)
			}
		})
	}
}

func TestSRExitBooksLossesAndBreakeven(t *testing.T) {
	s, st := srTest(t)
	ss, prev, cur := fadeSetup()
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no entry")
	}
	entry, risk := st.IndexEntry, st.Risk

	// +1R moves the stop to entry; a return to entry then stops out flat.
	up := srCandle(entry + risk)
	st.LastClose = up.Close
	if sig := s.evaluateExit(up, st); sig != nil {
		t.Fatalf("exited at +1R: %s", sig.Reason)
	}
	if !st.Breakeven || st.StopLevel != entry {
		t.Fatalf("breakeven=%v stop=%d entry=%d", st.Breakeven, st.StopLevel, entry)
	}
	back := srCandle(entry)
	st.LastClose = back.Close
	sig := s.evaluateExit(back, st)
	if sig == nil || sig.Action != ActionExit || sig.Side != SideCall {
		t.Fatalf("no breakeven exit: %+v", sig)
	}
	if st.ConsecLosses != 0 || st.DayPnLPts != 0 || st.CooldownLeft != s.cfg.CooldownCandles {
		t.Errorf("losses=%d pnl=%d cooldown=%d", st.ConsecLosses, st.DayPnLPts, st.CooldownLeft)
	}

	// A stopped-out trade counts as a loss.
	st.CooldownLeft = 0
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no second entry")
	}
	down := srCandle(st.StopLevel - 100)
	st.LastClose = down.Close
	if sig := s.evaluateExit(down, st); sig == nil || !strings.Contains(sig.Reason, "STOP") {
		t.Fatalf("no stop exit: %+v", sig)
	}
	if st.ConsecLosses != 1 || st.DayPnLPts >= 0 {
		t.Errorf("losses=%d pnl=%d", st.ConsecLosses, st.DayPnLPts)
	}
}

func TestSRPremiumHardSL(t *testing.T) {
	s, st := srTest(t)
	ss, prev, cur := fadeSetup()
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no entry")
	}
	st.LastClose = cur.Close
	s.SetPositionToken("NFO:4242")
	s.SetFNOEntryPrice(10000)
	if sig := s.OnTick(model.Tick{Exchange: "NFO", Token: "4242", Price: 8500}); sig != nil {
		t.Fatalf("exited at -15%%: %s", sig.Reason)
	}
	sig := s.OnTick(model.Tick{Exchange: "NFO", Token: "4242", Price: 7900})
	if sig == nil || sig.Action != ActionExit || !strings.Contains(sig.Reason, "HARD SL") {
		t.Fatalf("no hard SL: %+v", sig)
	}
	if st.Side != SideNone {
		t.Error("position still open")
	}
}

func TestSRCancelEntryRestoresBudget(t *testing.T) {
	s, st := srTest(t)
	ss, prev, cur := fadeSetup()
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no entry")
	}
	s.CancelEntry(SideCall, "no contract")
	if st.Side != SideNone || st.TradesToday != 0 || st.CooldownLeft != 0 {
		t.Fatalf("side=%s trades=%d cooldown=%d", st.Side, st.TradesToday, st.CooldownLeft)
	}
}

func TestSRRegimeClassifier(t *testing.T) {
	sc := newSRContext(DefaultSRContextConfig())
	base := SRState{
		Range: RangeState{ADXReady: true, ATR15: pts(20)},
		VWAP:  pts(22830), EMAFast: pts(22830), EMASlow: pts(22830), EMAReady: true,
	}
	cases := []struct {
		name string
		mod  func(*SRState)
		px   int64
		want SRRegime
	}{
		{"not ready", func(s *SRState) { s.EMAReady = false }, pts(22830), SRRegimeNone},
		{"dead", func(s *SRState) { s.Range.ATR15 = pts(8) }, pts(22830), SRRegimeDead},
		{"range", func(s *SRState) { s.Range.ADX = 18; s.Crosses = 3 }, pts(22830), SRRegimeRange},
		{"range needs crosses", func(s *SRState) { s.Range.ADX = 18; s.Crosses = 1 }, pts(22830), SRRegimeMixed},
		{"trend up", func(s *SRState) {
			s.Range.ADX = 30
			s.EMAFast, s.VWAPSlope = pts(22840), pts(20)
		}, pts(22860), SRRegimeTrendUp},
		{"trend down against 1h", func(s *SRState) {
			s.Range.ADX, s.Range.HTFTrend = 30, 1
			s.EMAFast, s.VWAPSlope = pts(22820), -pts(20)
		}, pts(22800), SRRegimeMixed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := base
			tc.mod(&st)
			if got := sc.regime(st, tc.px); got != tc.want {
				t.Errorf("regime=%s want %s", got, tc.want)
			}
		})
	}
}

// End to end on the range wave: warmup, levels, VWAP and a snapshot
// round trip on real 1m flow.
func TestSRWaveEndToEndAndSnapshot(t *testing.T) {
	cfg := DefaultNifty50SRConfig()
	cfg.IndexToken = ""
	s := NewNifty50SR(65, cfg)
	f := newRangeFeed()
	s.Warmup(f.warmDays(3))

	var sigs []Signal
	for _, ts := range sessionMinutes(f.day) {
		if sig := s.OnTFCandle(tfc(ts, f.bar(f.next()))); sig != nil {
			sigs = append(sigs, *sig)
		}
	}
	for _, sig := range sigs {
		if sig.StrategyName != "NIFTY50_SR" || sig.Side == SideNone {
			t.Fatalf("bad signal %+v", sig)
		}
	}
	ss, ok := s.SRState("NSE:NIFTY")
	if !ok || ss.VWAP == 0 || len(ss.Levels) == 0 || ss.ORHigh == 0 {
		t.Fatalf("view not built: vwap=%d levels=%d or=%d", ss.VWAP, len(ss.Levels), ss.ORHigh)
	}
	if last := sigs; len(last) > 0 && last[len(last)-1].Action != ActionExit {
		t.Errorf("day ended with an open position: %+v", last[len(last)-1])
	}

	data, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := NewNifty50SR(65, cfg)
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	rs, _ := r.SRState("NSE:NIFTY")
	if rs.VWAP != ss.VWAP || len(rs.Levels) != len(ss.Levels) || rs.EMAReady != ss.EMAReady || rs.Regime != ss.Regime {
		t.Errorf("restored view differs: vwap %d/%d levels %d/%d regime %s/%s",
			rs.VWAP, ss.VWAP, len(rs.Levels), len(ss.Levels), rs.Regime, ss.Regime)
	}
	t.Logf("wave day: %d signals, regime=%s, rejects=%v", len(sigs), ss.Regime, s.RejectStats())
}

func TestSREntryTimeframe(t *testing.T) {
	s, st := srTest(t)
	if !s.entryBarClosed(st) || s.retestBars() != 30 {
		t.Fatalf("1m default: closed=%v retest bars=%d", s.entryBarClosed(st), s.retestBars())
	}
	s.cfg.EntryTFMinutes = 5
	if s.entryBarClosed(st) || s.retestBars() != 6 {
		t.Fatalf("5m: closed=%v retest bars=%d", s.entryBarClosed(st), s.retestBars())
	}
}

func TestSRForceExitStaleKeepsTodaysPosition(t *testing.T) {
	s, st := srTest(t)
	st.Side, st.IndexEntry, st.StopLevel, st.TargetLevel = SideCall, pts(22756), pts(22715), pts(22848)

	if sigs := s.ForceExitStale("2026-09-28", "STALE"); len(sigs) != 0 {
		t.Fatalf("same-day position must survive a restart, got %+v", sigs)
	}
	if st.Side != SideCall {
		t.Fatalf("position reset: side=%s", st.Side)
	}

	sigs := s.ForceExitStale("2026-09-29", "STALE")
	if len(sigs) != 1 || sigs[0].Action != ActionExit || sigs[0].Side != SideCall {
		t.Fatalf("previous-day position must exit, got %+v", sigs)
	}
	if st.Side != SideNone {
		t.Fatalf("position not reset: side=%s", st.Side)
	}
}

func TestSRNoProgressExit(t *testing.T) {
	s, st := srTest(t)
	s.cfg.NoProgressMin, s.cfg.NoProgressRPct = 30, 50
	ss, prev, cur := fadeSetup()
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no entry")
	}
	entry, risk := st.IndexEntry, st.Risk
	at := func(min int, close int64) model.TFCandle {
		c := srCandle(close)
		c.TS = srNow.Add(time.Duration(min) * time.Minute)
		st.LastClose = close
		return c
	}

	// Drifting below +0.5R: held until 30 minutes have passed, then exited.
	if sig := s.evaluateExit(at(10, entry+risk*40/100), st); sig != nil {
		t.Fatalf("exited early: %s", sig.Reason)
	}
	if sig := s.evaluateExit(at(29, entry+risk*10/100), st); sig != nil {
		t.Fatalf("exited before 30 min: %s", sig.Reason)
	}
	sig := s.evaluateExit(at(30, entry+risk*10/100), st)
	if sig == nil || !strings.Contains(sig.Reason, "NO PROGRESS") {
		t.Fatalf("no no-progress exit: %+v", sig)
	}

	// A trade that reached +0.5R once is left to its stops.
	st.CooldownLeft = 0
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no second entry")
	}
	entry, risk = st.IndexEntry, st.Risk
	if sig := s.evaluateExit(at(5, entry+risk*60/100), st); sig != nil {
		t.Fatalf("exited at +0.6R: %s", sig.Reason)
	}
	if sig := s.evaluateExit(at(40, entry+risk*10/100), st); sig != nil {
		t.Fatalf("exited after progress: %s", sig.Reason)
	}
}

func TestSRNoProgressOffByDefault(t *testing.T) {
	s, st := srTest(t)
	ss, prev, cur := fadeSetup()
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no entry")
	}
	c := srCandle(st.IndexEntry)
	c.TS = srNow.Add(2 * time.Hour)
	st.LastClose = c.Close
	if sig := s.evaluateExit(c, st); sig != nil {
		t.Fatalf("exited with no-progress off: %s", sig.Reason)
	}
}

func TestSRReentryNeedsStrongerTrend(t *testing.T) {
	s, st := srTest(t)
	s.cfg.ReentryMinADX = 30
	ss, prev, cur := fadeSetup() // ADX 18
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("first entry refused")
	}
	st.LastClose = cur.Close
	s.exit("NSE", "NIFTY", st, "test")
	st.CooldownLeft = 0

	// Same side, ADX no higher than at the last entry and below 30: refused.
	if sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("re-entry on a weakening trend: %s", sig.Reason)
	}
	if s.rejects["fade:reentry_adx"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}

	// ADX rising since the last entry: allowed.
	ss.Range.ADX = 21
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("re-entry with rising ADX refused; rejects=%v", s.rejects)
	}
}

// withDay sets today's open/high/low (index points) on a view.
func withDay(ss SRState, open, high, low int64) SRState {
	ss.DayOpen, ss.DayHigh, ss.DayLow = pts(open), pts(high), pts(low)
	return ss
}

func pullbackSetup() (SRState, ohlcv, ohlcv) {
	ss := srView(SRRegimeTrendUp, pts(22820), 50)
	ss.EMAFast, ss.EMASlow = pts(22828), pts(22815)
	ss.Levels = []SRLevel{{Price: pts(22700), Touches: 2}, {Price: pts(22900), Touches: 2}}
	prev := ohlcv{Open: pts(22832), High: pts(22833), Low: pts(22822), Close: pts(22824)}
	cur := ohlcv{Open: pts(22823), High: pts(22836), Low: pts(22818), Close: pts(22835)}
	return ss, prev, cur
}

func dayTest(t *testing.T) (*Nifty50SR, *nifty50SRState) {
	s, st := srTest(t)
	s.cfg.DayExtremePct, s.cfg.DayRunPts, s.cfg.DayExtremePuts = 80, pts(100), true
	s.cfg.DayExtremeFromMin = 0
	s.cfg.GateMode = SRGateBlock
	return s, st
}

// 13:12 on 2026-10-06: a CALL bought near the day high after a 100+ pt run.
func TestSRDayExtremeRefusesCallAtTheTop(t *testing.T) {
	s, st := dayTest(t)
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22730, 22840, 22720) // close 22,835: 96% of range, 115 pts off the low
	if sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("CALL at the day high: %s", sig.Reason)
	}
	if s.rejects["pullback:day_extreme"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func TestSRDayExtremeAllowsCallMidRange(t *testing.T) {
	s, st := dayTest(t)
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22700, 23000, 22720) // 41% of range
	ss.PrevDayHigh, ss.PrevDayLow = pts(22820), pts(22600)
	ss.ORHigh, ss.ORLow = pts(22760), pts(22720)
	sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil {
		t.Fatalf("mid-range CALL refused; rejects=%v", s.rejects)
	}
	want := "day=pos:41% hi:-165 lo:+115 pdh:+15 pdl:+235 open:+135 or:above"
	if !strings.Contains(sig.Reason, want) {
		t.Errorf("reason %q lacks %q", sig.Reason, want)
	}
}

func TestSRDayExtremeNeedsABigRun(t *testing.T) {
	s, st := dayTest(t)
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22800, 22840, 22800) // 88% but only 35 pts off the low
	if s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("small-day CALL refused; rejects=%v", s.rejects)
	}
}

func TestSRDayExtremeRefusesFadeCallAtTheTop(t *testing.T) {
	s, st := dayTest(t)
	ss, prev, cur := fadeSetup() // close 22,816
	ss = withDay(ss, 22710, 22820, 22700)
	if sig := s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("fade CALL at the day high: %s", sig.Reason)
	}
	if s.rejects["fade:day_extreme"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func TestSRDayExtremeRefusesPutAtTheBottom(t *testing.T) {
	s, st := dayTest(t)
	ss := srView(SRRegimeMixed, pts(22825), 40)
	ss.EMAFast, ss.EMASlow = pts(22800), pts(22820)
	ss.Levels = append(ss.Levels, SRLevel{Price: pts(22760), Touches: 2, Source: SRLevelSwing})
	ss = withDay(ss, 22900, 22920, 22795) // close 22,801: 5% of range, 119 pts off the high
	st.PendingSide, st.PendingLevel, st.PendingLeft = SidePut, pts(22810), 6
	brk := ohlcv{Open: pts(22812), High: pts(22813), Low: pts(22800), Close: pts(22802)}
	retest := ohlcv{Open: pts(22804), High: pts(22811), Low: pts(22800), Close: pts(22801)}
	if sig := s.evaluateRetest(srCandle(retest.Close), st, ss, brk, retest, srMin); sig != nil {
		t.Fatalf("PUT at the day low: %s", sig.Reason)
	}
	if s.rejects["retest:day_extreme"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}

	// PUT mirror off (the default): the same PUT is taken.
	s2, st2 := dayTest(t)
	s2.cfg.DayExtremePuts = false
	st2.PendingSide, st2.PendingLevel, st2.PendingLeft = SidePut, pts(22810), 6
	if s2.evaluateRetest(srCandle(retest.Close), st2, ss, brk, retest, srMin) == nil {
		t.Fatalf("PUT refused with the mirror off; rejects=%v", s2.rejects)
	}
}

func TestSRDayExtremeOff(t *testing.T) {
	s, st := srTest(t)
	s.cfg.DayExtremePct = 0
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22730, 22840, 22720)
	if s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("gate off still refused; rejects=%v", s.rejects)
	}
}

// 09:51 on 2026-10-06: early in the day any rally is "100% of range";
// the check waits for DayExtremeFromMin.
func TestSRDayExtremeWaitsForFromMin(t *testing.T) {
	s, st := dayTest(t)
	s.cfg.DayExtremeFromMin = srMin + 1
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22730, 22840, 22720)
	if s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("refused before the check starts; rejects=%v", s.rejects)
	}
}

func TestSRRetestTolerance(t *testing.T) {
	ss := srView(SRRegimeMixed, pts(22825), 40)
	ss.EMAFast, ss.EMASlow = pts(22800), pts(22820)
	ss.Levels = append(ss.Levels, SRLevel{Price: pts(22760), Touches: 2, Source: SRLevelSwing})
	brk := ohlcv{Open: pts(22812), High: pts(22813), Low: pts(22800), Close: pts(22802)}
	// High 7 pts under the broken 22,810 (inside the 10-pt touch, outside
	// 5), red with an upper wick longer than its body.
	loose := ohlcv{Open: pts(22799), High: pts(22803), Low: pts(22795), Close: pts(22796)}

	s, st := srTest(t)
	st.PendingSide, st.PendingLevel, st.PendingLeft = SidePut, pts(22810), 6
	if s.evaluateRetest(srCandle(loose.Close), st, ss, brk, loose, srMin) == nil {
		t.Fatalf("10-pt touch refused; rejects=%v", s.rejects)
	}

	s, st = srTest(t)
	s.cfg.RetestTolPts = pts(5)
	st.PendingSide, st.PendingLevel, st.PendingLeft = SidePut, pts(22810), 6
	if sig := s.evaluateRetest(srCandle(loose.Close), st, ss, brk, loose, srMin); sig != nil {
		t.Fatalf("loose retest taken with a 5-pt touch: %s", sig.Reason)
	}
	if st.PendingSide != SidePut {
		t.Error("pending break dropped; it should keep waiting for a real retest")
	}
}

// boxBars5 fills the context's 5m history with n bars spanning lo..hi.
func boxBars5(st *nifty50SRState, n int, lo, hi int64) {
	st.ctx.rc.bars5 = nil
	for i := 0; i < n; i++ {
		st.ctx.rc.bars5 = append(st.ctx.rc.bars5, tfBar{ohlcv: ohlcv{High: pts(hi), Low: pts(lo)}})
	}
}

// 12:45–13:55 on 2026-10-06: price boxed in ~35 pts while 15m ADX still
// read TREND_UP; the pullback CALL there went nowhere.
func TestSRSidewaysBoxRefusesTrendEntry(t *testing.T) {
	s, st := srTest(t)
	s.cfg.BoxBars, s.cfg.BoxATRPct, s.cfg.GateMode = 6, 150, SRGateBlock
	ss, prev, cur := pullbackSetup() // 15m ATR 20 pts → box limit 30 pts
	boxBars5(st, 6, 22810, 22838)    // 28-pt box
	if sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin); sig != nil {
		t.Fatalf("entered a sideways box: %s", sig.Reason)
	}
	if s.rejects["pullback:sideways"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}

	boxBars5(st, 6, 22790, 22838) // 48 pts: moving
	if s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("moving market refused; rejects=%v", s.rejects)
	}
}

func TestSRSidewaysBoxLeavesFades(t *testing.T) {
	s, st := srTest(t)
	s.cfg.BoxBars, s.cfg.BoxATRPct, s.cfg.GateMode = 6, 150, SRGateBlock
	ss, prev, cur := fadeSetup()
	boxBars5(st, 6, 22805, 22830)
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("fade refused in a box; rejects=%v", s.rejects)
	}
	s2, st2 := srTest(t)
	s2.cfg.BoxBars, s2.cfg.BoxATRPct, s2.cfg.BoxFadeToo, s2.cfg.GateMode = 6, 150, true, SRGateBlock
	boxBars5(st2, 6, 22805, 22830)
	if s2.evaluateFade(srCandle(cur.Close), st2, ss, prev, cur, srMin) != nil {
		t.Fatal("fade taken with BoxFadeToo")
	}
}

// Warn mode (the default): the entry is taken and flagged, not refused.
func TestSRSituationWarnsInsteadOfBlocking(t *testing.T) {
	s, st := dayTest(t)
	s.cfg.GateMode = SRGateWarn
	s.cfg.BoxBars, s.cfg.BoxATRPct = 6, 150
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22730, 22840, 22720) // 96% of the day after 115 pts
	boxBars5(st, 6, 22810, 22838)         // 28-pt box, limit 30
	sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil {
		t.Fatalf("warn mode refused the entry; rejects=%v", s.rejects)
	}
	if strings.Join(sig.Warnings, ",") != "sideways,day_extreme" {
		t.Errorf("warnings %v", sig.Warnings)
	}
	if !strings.HasSuffix(sig.Reason, " warn=sideways,day_extreme") {
		t.Errorf("reason %q", sig.Reason)
	}
	if s.dayWarns["sideways"] != 1 || s.dayWarns["day_extreme"] != 1 || len(s.rejects) != 0 {
		t.Errorf("dayWarns=%v rejects=%v", s.dayWarns, s.rejects)
	}
}

func TestSRSituationNoWarningWhenClear(t *testing.T) {
	s, st := dayTest(t)
	s.cfg.GateMode = SRGateWarn
	ss, prev, cur := pullbackSetup()
	ss = withDay(ss, 22700, 23000, 22720)
	sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil || len(sig.Warnings) != 0 || strings.Contains(sig.Reason, "warn=") {
		t.Fatalf("clear entry flagged: %+v", sig)
	}
}

func TestSRDefaultGateModeIsWarn(t *testing.T) {
	if m := DefaultNifty50SRConfig().GateMode; m != SRGateWarn {
		t.Fatalf("default GateMode %q, want warn", m)
	}
}

func TestSRExitRequested(t *testing.T) {
	s, st := srTest(t)
	ss, prev, cur := pullbackSetup()
	if s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatalf("setup entry refused; rejects=%v", s.rejects)
	}
	st.LastClose, st.FNOToken = cur.Close, "NFO:45001"

	if s.ExitRequested(SidePut, "45001", "x") != nil {
		t.Fatal("closed a CALL on a PUT request")
	}
	if s.ExitRequested(SideCall, "45999", "x") != nil {
		t.Fatal("closed the position on another contract's request")
	}
	sig := s.ExitRequested(SideCall, "45001", "EXITWATCH REVERSAL: p=0.81")
	if sig == nil || sig.Action != ActionExit || sig.Side != SideCall || sig.Reason != "EXITWATCH REVERSAL: p=0.81" {
		t.Fatalf("exit signal %+v", sig)
	}
	if st.Side != SideNone {
		t.Fatal("position still open after the exit")
	}
	if s.ExitRequested(SideCall, "45001", "again") != nil {
		t.Fatal("second request closed a flat book")
	}
}
