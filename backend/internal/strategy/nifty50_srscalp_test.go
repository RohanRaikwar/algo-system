package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// ── Fixture ──
// Support 22,800 (3 touches, held once today), resistance 22,900 (3 touches,
// held once today), VWAP 22,820, 5m EMA fast above slow: an uptrend view in
// which a bounce at support is a with-trend CALL.

func scalpTest(t *testing.T) (*Nifty50SRScalp, *srScalpState) {
	t.Helper()
	cfg := DefaultNifty50SRScalpConfig()
	cfg.IndexToken = ""
	s := NewNifty50SRScalp(65, cfg)
	st := s.getOrCreate("NSE:NIFTY")
	st.TradeDay = "2026-09-28"
	st.DayLevels = []scalpLevel{{Price: pts(22800), Held: 1}, {Price: pts(22900), Held: 1}}
	return s, st
}

func scalpView() SRState {
	return SRState{
		Range:   RangeState{ADX: 20, ADXReady: true, ATR15: pts(20)},
		Regime:  SRRegimeMixed,
		VWAP:    pts(22820),
		EMAFast: pts(22840), EMASlow: pts(22830), EMAReady: true,
		Levels: []SRLevel{
			{Price: pts(22800), Touches: 3, Source: SRLevelSwing},
			{Price: pts(22900), Touches: 3, Source: SRLevelSwing},
		},
	}
}

// scalpDownView is the mirror: trend down, VWAP 22,880.
func scalpDownView() SRState {
	ss := scalpView()
	ss.VWAP = pts(22880)
	ss.EMAFast, ss.EMASlow = pts(22860), pts(22870)
	return ss
}

func scalpCandleAt(min int, close int64) model.TFCandle {
	ts := time.Date(2026, 9, 28, 0, 0, 0, 0, ist).Add(time.Duration(min-1) * time.Minute)
	return model.TFCandle{Token: "NIFTY", Exchange: "NSE", TF: tf1m, TS: ts, Close: close}
}

// Paise helper for fractional points: pp(22796, 50) = 22,796.50.
func pp(p, frac int64) int64 { return p*100 + frac }

// bounce is a bullish 1m bar whose low tags 22,801 and closes 22,805.
var bounce = ohlcv{Open: pts(22802), High: pts(22806), Low: pts(22801), Close: pts(22805)}

// rejection is a bearish 1m bar whose high tags 22,899 and closes 22,895.
var rejection = ohlcv{Open: pts(22898), High: pts(22899), Low: pts(22894), Close: pts(22895)}

const scalpMin = 10*60 + 30

func scalpEnter(s *Nifty50SRScalp, st *srScalpState, ss SRState, cur ohlcv, min int) *Signal {
	return s.evaluateEntry(scalpCandleAt(min, cur.Close), st, ss, cur, min)
}

// ── Entry rules ──

func TestScalpCallOnSupportBounce(t *testing.T) {
	s, st := scalpTest(t)
	sig := scalpEnter(s, st, scalpView(), bounce, scalpMin)
	if sig == nil {
		t.Fatalf("no entry; rejects=%v", s.rejects)
	}
	if sig.Action != ActionBuy || sig.Side != SideCall || sig.StrategyName != "NIFTY50_SRSCALP" {
		t.Fatalf("signal %+v", sig)
	}
	if sig.Strike != 22800 {
		t.Errorf("strike=%d, want ATM 22800", sig.Strike)
	}
	// ₹3 premium at 0.5 delta ≈ 6 index points.
	if sig.TargetMove != pts(6) {
		t.Errorf("target move=%d, want %d", sig.TargetMove, pts(6))
	}
	if !strings.HasPrefix(sig.Reason, "SRSCALP CALL") {
		t.Errorf("reason %q", sig.Reason)
	}
	if st.Side != SideCall || st.Level != pts(22800) || st.TradesToday != 1 {
		t.Errorf("state side=%s level=%d trades=%d", st.Side, st.Level, st.TradesToday)
	}
}

func TestScalpPutOnResistanceRejection(t *testing.T) {
	s, st := scalpTest(t)
	sig := scalpEnter(s, st, scalpDownView(), rejection, scalpMin)
	if sig == nil {
		t.Fatalf("no entry; rejects=%v", s.rejects)
	}
	if sig.Side != SidePut || st.Level != pts(22900) || sig.Strike != 22900 {
		t.Fatalf("side=%s level=%d strike=%d", sig.Side, st.Level, sig.Strike)
	}
}

func TestScalpLowMustBeInZone(t *testing.T) {
	s, st := scalpTest(t)
	// Low 22,803 is 3 pts above support: outside the 2-pt zone.
	bar := ohlcv{Open: pts(22804), High: pts(22808), Low: pts(22803), Close: pts(22807)}
	if sig := scalpEnter(s, st, scalpView(), bar, scalpMin); sig != nil {
		t.Fatalf("entered off the level: %s", sig.Reason)
	}
}

func TestScalpNoEntryWhenWickBreaksLevel(t *testing.T) {
	s, st := scalpTest(t)
	// Low 22,796.50 is 3.5 pts under support: beyond the 3-pt break.
	bar := ohlcv{Open: pts(22802), High: pts(22806), Low: pp(22796, 50), Close: pts(22805)}
	if sig := scalpEnter(s, st, scalpView(), bar, scalpMin); sig != nil {
		t.Fatalf("entered on a broken level: %s", sig.Reason)
	}
	// 3 pts under exactly is still a hold.
	bar.Low = pts(22797)
	if sig := scalpEnter(s, st, scalpView(), bar, scalpMin); sig == nil {
		t.Fatalf("no entry at the break limit; rejects=%v", s.rejects)
	}
}

func TestScalpNeedsBullishCloseAboveLevel(t *testing.T) {
	s, st := scalpTest(t)
	red := ohlcv{Open: pts(22805), High: pts(22806), Low: pts(22801), Close: pts(22803)}
	if sig := scalpEnter(s, st, scalpView(), red, scalpMin); sig != nil {
		t.Fatalf("entered on a red bar: %s", sig.Reason)
	}
	under := ohlcv{Open: pts(22798), High: pts(22801), Low: pts(22798), Close: pts(22799)}
	if sig := scalpEnter(s, st, scalpView(), under, scalpMin); sig != nil {
		t.Fatalf("entered closing under support: %s", sig.Reason)
	}
}

func TestScalpRetiresBrokenLevel(t *testing.T) {
	s, st := scalpTest(t)
	ss := scalpView()
	// Price above support drops 4 pts through it: broken for the day.
	s.trackLevels(st, ss, pts(22810), ohlcv{Open: pts(22808), High: pts(22809), Low: pts(22796), Close: pts(22798)})
	if !st.DayLevels[0].Broken {
		t.Fatalf("level not retired: %+v", st.DayLevels)
	}
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig != nil {
		t.Fatalf("entered on a retired level: %s", sig.Reason)
	}
	if s.rejects["level_broken"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func TestScalpTrackLevelsCountsHolds(t *testing.T) {
	s, st := scalpTest(t)
	st.DayLevels = nil
	ss := scalpView()
	// Support tagged and held; resistance tagged and rejected.
	s.trackLevels(st, ss, pts(22810), bounce)
	s.trackLevels(st, ss, pts(22890), rejection)
	sup, res := st.dayLevel(pts(22800), false), st.dayLevel(pts(22900), false)
	if sup == nil || sup.Held != 1 || sup.Broken {
		t.Errorf("support %+v", sup)
	}
	if res == nil || res.Held != 1 || res.Broken {
		t.Errorf("resistance %+v", res)
	}
}

func TestScalpOneTradePerLevel(t *testing.T) {
	s, st := scalpTest(t)
	if scalpEnter(s, st, scalpView(), bounce, scalpMin) == nil {
		t.Fatal("first entry refused")
	}
	s.resetPosition(st)
	if sig := scalpEnter(s, st, scalpView(), bounce, scalpMin+5); sig != nil {
		t.Fatalf("second trade on the same level: %s", sig.Reason)
	}
	if s.rejects["level_traded"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
}

func TestScalpStrongLevelsOnly(t *testing.T) {
	s, st := scalpTest(t)
	ss := scalpView()
	ss.Levels[0].Touches = 2
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig != nil {
		t.Fatalf("entered on a 2-touch level with no confluence: %s", sig.Reason)
	}
	if s.rejects["weak_level"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
	for name, mod := range map[string]func(*SRState){
		"prev-day low": func(v *SRState) { v.PrevDayLow = pts(22804) },
		"opening low":  func(v *SRState) { v.ORLow = pts(22796) },
		"vwap":         func(v *SRState) { v.VWAP = pts(22805) },
	} {
		s, st := scalpTest(t)
		v := scalpView()
		v.Levels[0].Touches = 2
		mod(&v)
		if sig := scalpEnter(s, st, v, bounce, scalpMin); sig == nil {
			t.Errorf("%s confluence: no entry; rejects=%v", name, s.rejects)
		}
	}
	s, st = scalpTest(t)
	s.cfg.StrongLevels = false
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig == nil {
		t.Errorf("StrongLevels off: no entry; rejects=%v", s.rejects)
	}
}

func TestScalpLevelMustHaveHeldToday(t *testing.T) {
	s, st := scalpTest(t)
	st.DayLevels = nil
	if sig := scalpEnter(s, st, scalpView(), bounce, scalpMin); sig != nil {
		t.Fatalf("entered on the first touch of the day: %s", sig.Reason)
	}
	if s.rejects["not_held"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
	s, st = scalpTest(t)
	st.DayLevels = nil
	s.cfg.HeldToday = false
	if sig := scalpEnter(s, st, scalpView(), bounce, scalpMin); sig == nil {
		t.Errorf("HeldToday off: no entry; rejects=%v", s.rejects)
	}
}

func TestScalpWithTrendOnly(t *testing.T) {
	s, st := scalpTest(t)
	ss := scalpView()
	ss.EMAFast = pts(22820) // fast below slow
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig != nil {
		t.Fatalf("CALL against the 5m EMAs: %s", sig.Reason)
	}
	ss = scalpView()
	ss.VWAP = pts(22810) // close 22,805 below VWAP
	ss.Levels[0].Touches = 3
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig != nil {
		t.Fatalf("CALL under VWAP: %s", sig.Reason)
	}
	if s.rejects["against_trend"] != 2 {
		t.Errorf("rejects=%v", s.rejects)
	}
	// A PUT in the uptrend view is refused too.
	if sig := scalpEnter(s, st, scalpView(), rejection, scalpMin); sig != nil {
		t.Fatalf("PUT against the trend: %s", sig.Reason)
	}
	s.cfg.WithTrend = false
	ss.EMAFast = pts(22820)
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig == nil {
		t.Errorf("WithTrend off: no entry; rejects=%v", s.rejects)
	}
}

func TestScalpSkipsStrongCounterTrendRegime(t *testing.T) {
	s, st := scalpTest(t)
	s.cfg.WithTrend = false
	ss := scalpView()
	ss.Regime, ss.Range.ADX = SRRegimeTrendDown, 31
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig != nil {
		t.Fatalf("CALL into a strong downtrend: %s", sig.Reason)
	}
	if s.rejects["counter_trend_regime"] != 1 {
		t.Errorf("rejects=%v", s.rejects)
	}
	ss.Range.ADX = 29 // not strong: allowed
	if sig := scalpEnter(s, st, ss, bounce, scalpMin); sig == nil {
		t.Errorf("weak downtrend: no entry; rejects=%v", s.rejects)
	}
	s, st = scalpTest(t)
	s.cfg.WithTrend = false
	ss = scalpView()
	ss.Regime, ss.Range.ADX = SRRegimeTrendUp, 35
	if sig := scalpEnter(s, st, ss, rejection, scalpMin); sig != nil {
		t.Fatalf("PUT into a strong uptrend: %s", sig.Reason)
	}
	s.cfg.SkipCounterTrend = false
	if sig := scalpEnter(s, st, ss, rejection, scalpMin); sig == nil {
		t.Errorf("SkipCounterTrend off: no entry; rejects=%v", s.rejects)
	}
}

func TestScalpDailyCapsAndWindow(t *testing.T) {
	s, st := scalpTest(t)
	st.TradesToday = 5
	if scalpEnter(s, st, scalpView(), bounce, scalpMin) != nil || s.rejects["trade_cap"] != 1 {
		t.Errorf("trade cap: rejects=%v", s.rejects)
	}
	s, st = scalpTest(t)
	st.ConsecLosses = 2
	if scalpEnter(s, st, scalpView(), bounce, scalpMin) != nil || s.rejects["consec_losses"] != 1 {
		t.Errorf("loss cap: rejects=%v", s.rejects)
	}
	for _, m := range []int{hhmm(9, 29), hhmm(14, 45)} {
		s, st = scalpTest(t)
		if sig := scalpEnter(s, st, scalpView(), bounce, m); sig != nil {
			t.Errorf("entered at %02d:%02d", m/60, m%60)
		}
	}
	s, st = scalpTest(t)
	if scalpEnter(s, st, scalpView(), bounce, hhmm(9, 30)) == nil {
		t.Errorf("09:30 refused; rejects=%v", s.rejects)
	}
}

func TestScalpOnePositionAtATime(t *testing.T) {
	s, st := scalpTest(t)
	if scalpEnter(s, st, scalpView(), bounce, scalpMin) == nil {
		t.Fatal("first entry refused")
	}
	if sig := scalpEnter(s, st, scalpDownView(), rejection, scalpMin+1); sig != nil {
		t.Fatalf("second position opened: %s", sig.Reason)
	}
}

func TestScalpCancelEntryRestoresBudget(t *testing.T) {
	s, st := scalpTest(t)
	if scalpEnter(s, st, scalpView(), bounce, scalpMin) == nil {
		t.Fatal("entry refused")
	}
	s.CancelEntry(SideCall, "no strike")
	if st.Side != SideNone || st.TradesToday != 0 {
		t.Fatalf("side=%s trades=%d", st.Side, st.TradesToday)
	}
	if l := st.dayLevel(pts(22800), false); l == nil || l.Traded {
		t.Fatalf("level still marked traded: %+v", l)
	}
	if scalpEnter(s, st, scalpView(), bounce, scalpMin+1) == nil {
		t.Errorf("re-entry refused; rejects=%v", s.rejects)
	}
}

func TestScalpDayResetClearsCapsAndLevels(t *testing.T) {
	s, st := scalpTest(t)
	st.TradesToday, st.ConsecLosses = 5, 2
	st.TradeDay = "2026-09-25"
	s.OnTFCandle(model.TFCandle{Token: "NIFTY", Exchange: "NSE", TF: tf1m,
		TS: time.Date(2026, 9, 28, 10, 0, 0, 0, ist), Open: pts(22800), High: pts(22801), Low: pts(22799), Close: pts(22800)})
	if st.TradesToday != 0 || st.ConsecLosses != 0 || len(st.DayLevels) != 0 || st.TradeDay != "2026-09-28" {
		t.Errorf("day not reset: trades=%d losses=%d levels=%v day=%s", st.TradesToday, st.ConsecLosses, st.DayLevels, st.TradeDay)
	}
}
