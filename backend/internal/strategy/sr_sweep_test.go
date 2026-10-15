package strategy

import (
	"strings"
	"testing"
	"time"
)

func TestDetectSweep(t *testing.T) {
	lv := func(p int64, src string, touches int) sweepLevel {
		return sweepLevel{price: pts(p), source: src, touches: touches}
	}
	bar := func(o, h, l, c int64) ohlcv {
		return ohlcv{Open: pts(o), High: pts(h), Low: pts(l), Close: pts(c)}
	}
	cases := []struct {
		name   string
		b      ohlcv
		levels []sweepLevel
		ok     bool
		side   string
		level  int64
		source string
		wick   int64
	}{
		{"high sweep", bar(24990, 25008, 24985, 24995), []sweepLevel{lv(25000, SRLevelSwing, 2)},
			true, SRSweepHigh, 25000, SRLevelSwing, 8},
		{"low sweep", bar(24010, 24015, 23993, 24004), []sweepLevel{lv(24000, SRSweepDayLow, 1)},
			true, SRSweepLow, 24000, SRSweepDayLow, 7},
		{"wick too short", bar(24990, 25004, 24985, 24995), []sweepLevel{lv(25000, SRLevelSwing, 2)}, false, "", 0, "", 0},
		{"close beyond is a break", bar(24990, 25010, 24985, 25006), []sweepLevel{lv(25000, SRLevelSwing, 2)}, false, "", 0, "", 0},
		{"open beyond is not a sweep", bar(25003, 25010, 24985, 24990), []sweepLevel{lv(25000, SRLevelSwing, 2)}, false, "", 0, "", 0},
		{"strongest source wins", bar(24990, 25020, 24985, 24995),
			[]sweepLevel{lv(25000, SRLevelSwing, 5), lv(25005, SRLevelPrevDay, 1), lv(25002, SRSweepDayHigh, 1)},
			true, SRSweepHigh, 25005, SRLevelPrevDay, 15},
		{"more touches break a tie", bar(24990, 25020, 24985, 24995),
			[]sweepLevel{lv(25000, SRLevelSwing, 2), lv(25004, SRLevelSwing, 4)},
			true, SRSweepHigh, 25004, SRLevelSwing, 16},
		{"both sides keep the bigger wick", bar(24500, 24520, 24470, 24502),
			[]sweepLevel{lv(24510, SRLevelPrevDay, 1), lv(24490, SRLevelSwing, 1)},
			true, SRSweepLow, 24490, SRLevelSwing, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sw, ok := detectSweep(tc.b, srNow, tc.levels, pts(5))
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (%+v)", ok, tc.ok, sw)
			}
			if !ok {
				return
			}
			if sw.Side != tc.side || sw.Level != pts(tc.level) || sw.Source != tc.source || sw.Wick != pts(tc.wick) {
				t.Errorf("got %s %d %s wick %d", sw.Side, sw.Level/100, sw.Source, sw.Wick/100)
			}
		})
	}
	if _, ok := detectSweep(bar(24990, 25050, 24985, 24995), srNow, []sweepLevel{lv(25000, SRLevelSwing, 2)}, 0); ok {
		t.Error("SweepMinWickPts 0 must turn detection off")
	}
}

func TestRecordSweepCooldownAndCap(t *testing.T) {
	st := &nifty50SRState{}
	sw := SRSweep{Side: SRSweepHigh, Level: pts(25000), TS: srNow}
	if !st.recordSweep(sw, 15*time.Minute) {
		t.Fatal("first sweep dropped")
	}
	sw.TS = srNow.Add(10 * time.Minute)
	if st.recordSweep(sw, 15*time.Minute) {
		t.Fatal("same level kept inside the cooldown")
	}
	sw.TS = srNow.Add(15 * time.Minute)
	if !st.recordSweep(sw, 15*time.Minute) {
		t.Fatal("same level dropped after the cooldown")
	}
	for i := int64(1); i <= 6; i++ {
		st.recordSweep(SRSweep{Level: pts(25000 + i*10), TS: srNow}, 15*time.Minute)
	}
	if len(st.Sweeps) != maxSweepsKept || st.Sweeps[len(st.Sweeps)-1].Level != pts(25060) {
		t.Fatalf("kept %d sweeps, last %+v", len(st.Sweeps), st.Sweeps[len(st.Sweeps)-1])
	}
}

// A CALL just after a high sweep is flagged and still taken, even in
// block mode.
func TestSRSweepWarnsCallNeverRefuses(t *testing.T) {
	s, st := srTest(t)
	s.cfg.GateMode = SRGateBlock
	ss, prev, cur := pullbackSetup()
	st.Sweeps = []SRSweep{{Side: SRSweepHigh, Level: pts(22840), TS: srNow.Add(-3 * time.Minute)}}
	sig := s.evaluatePullback(srCandle(cur.Close), st, ss, prev, cur, srMin)
	if sig == nil {
		t.Fatalf("sweep refused the entry; rejects=%v", s.rejects)
	}
	if strings.Join(sig.Warnings, ",") != "sweep_high" || !strings.HasSuffix(sig.Reason, " warn=sweep_high") {
		t.Errorf("warnings %v reason %q", sig.Warnings, sig.Reason)
	}
}

func TestSRSweepWarningWindowAndSide(t *testing.T) {
	s, st := srTest(t)
	st.Sweeps = []SRSweep{{Side: SRSweepLow, Level: pts(22800), TS: srNow.Add(-2 * time.Minute)}}
	if w := s.sweepWarning(st, SideCall, srNow); w != "" {
		t.Errorf("low sweep flagged a CALL: %q", w)
	}
	if w := s.sweepWarning(st, SidePut, srNow); w != "sweep_low" {
		t.Errorf("PUT after low sweep: %q", w)
	}
	if w := s.sweepWarning(st, SidePut, srNow.Add(3*time.Minute)); w != "" {
		t.Errorf("sweep older than SweepWarnMin still flags: %q", w)
	}
	s.cfg.SweepWarnMin = 0
	if w := s.sweepWarning(st, SidePut, srNow); w != "" {
		t.Errorf("SweepWarnMin 0 still flags: %q", w)
	}
}

// End to end: a bar wicking above the day high and closing back below is
// a sweep on the view; the day's first bar never is. The 10:00 bar is
// also the whole opening range here, and OPENING outranks DAY_HIGH.
func TestSRSweepOnView(t *testing.T) {
	cfg := DefaultNifty50SRConfig()
	cfg.IndexToken = ""
	s := NewNifty50SR(65, cfg)
	t0 := time.Date(2026, 10, 15, 10, 0, 0, 0, ist)
	bars := []ohlcv{
		{Open: pts(25000), High: pts(25010), Low: pts(24995), Close: pts(25005)},
		{Open: pts(25005), High: pts(25018), Low: pts(25001), Close: pts(25006)}, // 8 pts over 25,010, back below
	}
	for i, b := range bars {
		s.OnTFCandle(tfc(t0.Add(time.Duration(i)*time.Minute), b))
	}
	v, ok := s.View("NSE:NIFTY")
	if !ok || len(v.Sweeps) != 1 {
		t.Fatalf("sweeps %+v", v.Sweeps)
	}
	sw := v.Sweeps[0]
	if sw.Side != SRSweepHigh || sw.Level != pts(25010) || sw.Source != SRLevelOpening || sw.Wick != pts(8) {
		t.Errorf("sweep %+v", sw)
	}
	if v.SweepWarnMin != cfg.SweepWarnMin {
		t.Errorf("sweep_warn_min %d", v.SweepWarnMin)
	}
	found := false
	for _, w := range v.Warn {
		found = found || w == "call:sweep_high"
	}
	if !found {
		t.Errorf("view warn %v lacks call:sweep_high", v.Warn)
	}

	// Next day: the first bar is not checked against yesterday's high, and
	// yesterday's sweeps are gone.
	next := t0.AddDate(0, 0, 1)
	s.OnTFCandle(tfc(next, ohlcv{Open: pts(25000), High: pts(25030), Low: pts(24990), Close: pts(25000)}))
	if v, _ := s.View("NSE:NIFTY"); len(v.Sweeps) != 0 {
		t.Errorf("sweeps carried into the next day: %+v", v.Sweeps)
	}
}
