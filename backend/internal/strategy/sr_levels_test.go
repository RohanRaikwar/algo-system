package strategy

import (
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

// srOpenFade opens the fade CALL and gives it contract NFO:4242 filled at
// premium 10000.
func srOpenFade(t *testing.T, s *Nifty50SR, st *nifty50SRState) {
	t.Helper()
	ss, prev, cur := fadeSetup()
	if s.evaluateFade(srCandle(cur.Close), st, ss, prev, cur, srMin) == nil {
		t.Fatal("no entry")
	}
	st.LastClose = cur.Close
	s.SetPositionToken("NFO:4242")
	s.SetFNOEntryPrice(10000)
}

func srTick(price int64) model.Tick {
	return model.Tick{Exchange: "NFO", Token: "4242", Price: price}
}

func TestSRPremiumTargetDefault(t *testing.T) {
	s, st := srTest(t)
	srOpenFade(t, s, st)
	if st.FNOTarget != 0 {
		t.Fatalf("target %d with FNOTargetPct 0", st.FNOTarget)
	}

	s, st = srTest(t)
	s.cfg.FNOTargetPct = 40
	srOpenFade(t, s, st)
	if st.FNOTarget != 14000 {
		t.Fatalf("target %d, want 14000", st.FNOTarget)
	}
	if sig := s.OnTick(srTick(13900)); sig != nil {
		t.Fatalf("exited below target: %s", sig.Reason)
	}
	sig := s.OnTick(srTick(14000))
	if sig == nil || sig.Action != ActionExit || !strings.Contains(sig.Reason, "FNO TARGET") {
		t.Fatalf("no target exit: %+v", sig)
	}
	if st.Side != SideNone || st.FNOTarget != 0 {
		t.Errorf("side=%s target=%d after exit", st.Side, st.FNOTarget)
	}
}

func TestSRManualPremiumSLReplacesHardSL(t *testing.T) {
	s, st := srTest(t)
	srOpenFade(t, s, st)
	s.OnTick(srTick(9500))
	if err := s.SetLevels(SideCall, "4242", LevelEdit{FNOSL: 7000}); err != nil {
		t.Fatal(err)
	}
	// -21% would hit the 20% hard SL; the manual stop at 7000 holds.
	if sig := s.OnTick(srTick(7900)); sig != nil {
		t.Fatalf("hard SL still active: %s", sig.Reason)
	}
	sig := s.OnTick(srTick(7000))
	if sig == nil || !strings.Contains(sig.Reason, "FNO MANUAL SL") {
		t.Fatalf("no manual SL exit: %+v", sig)
	}
}

func TestSRSetLevelsValidation(t *testing.T) {
	s, st := srTest(t)
	srOpenFade(t, s, st)
	s.OnTick(srTick(10000))
	close := st.LastClose
	stop, target := st.StopLevel, st.TargetLevel

	bad := []struct {
		name string
		side PositionSide
		tok  string
		e    LevelEdit
	}{
		{"wrong side", SidePut, "", LevelEdit{FNOSL: 9000}},
		{"wrong token", SideCall, "9999", LevelEdit{FNOSL: 9000}},
		{"premium sl above LTP", SideCall, "4242", LevelEdit{FNOSL: 10000}},
		{"premium target below LTP", SideCall, "4242", LevelEdit{FNOTarget: 9900}},
		{"call stop above close", SideCall, "4242", LevelEdit{IndexStop: close + 100}},
		{"call target below close", SideCall, "4242", LevelEdit{IndexTarget: close - 100}},
		{"good stop, bad target", SideCall, "4242", LevelEdit{IndexStop: close - 100, IndexTarget: close}},
	}
	for _, c := range bad {
		if err := s.SetLevels(c.side, c.tok, c.e); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	if st.StopLevel != stop || st.TargetLevel != target || st.ManualLevels || st.FNOManualSL != 0 {
		t.Fatalf("refused edit changed state: stop=%d target=%d manual=%v sl=%d", st.StopLevel, st.TargetLevel, st.ManualLevels, st.FNOManualSL)
	}
}

func TestSRSetLevelsPutSides(t *testing.T) {
	s, st := srTest(t)
	st.Side, st.LastClose = SidePut, pts(22850)
	if err := s.SetLevels(SidePut, "", LevelEdit{IndexStop: pts(22800)}); err == nil {
		t.Error("PUT stop below close accepted")
	}
	if err := s.SetLevels(SidePut, "", LevelEdit{IndexStop: pts(22870), IndexTarget: pts(22800)}); err != nil {
		t.Fatal(err)
	}
}

func TestSRManualIndexStopDisablesManagement(t *testing.T) {
	s, st := srTest(t)
	s.cfg.SupportStopMode = "off"
	srOpenFade(t, s, st)
	entry, risk := st.IndexEntry, st.Risk
	newStop := st.LastClose - 500
	if err := s.SetLevels(SideCall, "", LevelEdit{IndexStop: newStop}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.View("NSE:NIFTY"); !st.ManualLevels || (v.Key != "" && !v.Manual) {
		t.Fatal("manual flag not set")
	}

	// +1R would move an automatic stop to entry; a manual one stays put.
	up := srCandle(entry + risk)
	st.LastClose = up.Close
	if sig := s.evaluateExit(up, st); sig != nil {
		t.Fatalf("exited at +1R: %s", sig.Reason)
	}
	if st.Breakeven || st.StopLevel != newStop {
		t.Fatalf("manual stop moved: breakeven=%v stop=%d", st.Breakeven, st.StopLevel)
	}
	down := srCandle(newStop)
	st.LastClose = down.Close
	sig := s.evaluateExit(down, st)
	if sig == nil || !strings.Contains(sig.Reason, "STOP") || !strings.Contains(sig.Reason, "manual") {
		t.Fatalf("no manual stop exit: %+v", sig)
	}
	if st.ManualLevels {
		t.Error("manual flag kept after exit")
	}
}

func TestSRManualLevelsSnapshot(t *testing.T) {
	s, st := srTest(t)
	srOpenFade(t, s, st)
	st.ManualLevels, st.FNOManualSL, st.FNOTarget = true, 7000, 15000
	got := restoreNifty50SR(s.cfg, snapshotNifty50SR("NSE:NIFTY", st))
	if !got.ManualLevels || got.FNOManualSL != 7000 || got.FNOTarget != 15000 {
		t.Fatalf("restored manual=%v sl=%d target=%d", got.ManualLevels, got.FNOManualSL, got.FNOTarget)
	}
	pos := s.CurrentFNOPosition()
	if pos == nil || pos.ManualSL != 7000 || pos.Target != 15000 || !pos.Manual || pos.IndexStop != st.StopLevel {
		t.Fatalf("live position %+v", pos)
	}
}

func TestSRRemoveIndexLevels(t *testing.T) {
	s, st := srTest(t)
	srOpenFade(t, s, st)
	entry := st.IndexEntry
	if err := s.SetLevels(SideCall, "", LevelEdit{IndexTarget: -1}); err != nil {
		t.Fatal(err)
	}
	if st.TargetLevel != 0 || !st.ManualLevels {
		t.Fatalf("target=%d manual=%v after remove", st.TargetLevel, st.ManualLevels)
	}
	far := srCandle(entry + pts(500))
	st.LastClose = far.Close
	if sig := s.evaluateExit(far, st); sig != nil {
		t.Fatalf("exited with no target: %s", sig.Reason)
	}

	if err := s.SetLevels(SideCall, "", LevelEdit{IndexStop: -1}); err != nil {
		t.Fatal(err)
	}
	low := srCandle(entry - pts(500))
	st.LastClose = low.Close
	if sig := s.evaluateExit(low, st); sig != nil {
		t.Fatalf("exited with no stop: %s", sig.Reason)
	}
}

func TestSRTrailStop(t *testing.T) {
	for _, side := range []PositionSide{SideCall, SidePut} {
		s, st := srTest(t)
		sign := int64(1)
		if side == SidePut {
			sign = -1
		}
		start := pts(22800)
		st.Side, st.IndexEntry, st.LastClose = side, start, start
		st.StopLevel, st.TargetLevel = start-sign*pts(50), start+sign*pts(200)
		if err := s.SetLevels(side, "", LevelEdit{IndexTrail: pts(20)}); err != nil {
			t.Fatal(err)
		}
		if want := start - sign*pts(20); st.StopLevel != want {
			t.Fatalf("%s: stop %d on set, want %d", side, st.StopLevel, want)
		}
		step := func(close int64) *Signal {
			c := srCandle(close)
			st.LastClose = c.Close
			return s.evaluateExit(c, st)
		}
		if sig := step(start + sign*pts(30)); sig != nil {
			t.Fatalf("%s: exited in favour: %s", side, sig.Reason)
		}
		high := start + sign*pts(10)
		if st.StopLevel != high {
			t.Fatalf("%s: stop %d after move, want %d", side, st.StopLevel, high)
		}
		// Pullback: the stop never moves against the position.
		if sig := step(start + sign*pts(15)); sig != nil {
			t.Fatalf("%s: exited above stop: %s", side, sig.Reason)
		}
		if st.StopLevel != high {
			t.Fatalf("%s: stop moved back to %d", side, st.StopLevel)
		}
		got := restoreNifty50SR(s.cfg, snapshotNifty50SR("NSE:NIFTY", st))
		if got.TrailDist != pts(20) {
			t.Fatalf("%s: restored trail %d", side, got.TrailDist)
		}
		if v, ok := s.View("NSE:NIFTY"); ok && v.Key != "" && v.Trail != pts(20) {
			t.Fatalf("%s: view trail %d", side, v.Trail)
		}
		sig := step(high)
		if sig == nil || !strings.Contains(sig.Reason, "STOP") {
			t.Fatalf("%s: no trailed stop exit: %+v", side, sig)
		}
		if st.TrailDist != 0 {
			t.Errorf("%s: trail kept after exit", side)
		}
	}
}

func TestSRTrailTooWide(t *testing.T) {
	s, st := srTest(t)
	st.Side, st.LastClose = SideCall, pts(22800)
	if err := s.SetLevels(SideCall, "", LevelEdit{IndexTrail: pts(2000)}); err == nil {
		t.Fatal("wide trail accepted")
	}
}
