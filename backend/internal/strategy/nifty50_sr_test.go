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
