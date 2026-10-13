package strategy

import (
	"strings"
	"testing"
)

// supportStopCall opens a CALL shaped like the 2026-10-13 trade: entry
// 22497, stop 22447 (R 50), strong support 22483 (×3) under entry, a weak
// level (×2) just under it.
func supportStopCall(t *testing.T, mode string) (*Nifty50SR, *nifty50SRState) {
	t.Helper()
	s, st := srTest(t)
	s.cfg.SupportStopMode, s.cfg.SupportStopZonePts = mode, pts(10)
	st.ctx.state = SRState{
		Range: RangeState{ATR15: pts(38)},
		Levels: []SRLevel{
			{Price: pts(22483), Touches: 3, Source: SRLevelSwing},
			{Price: pts(22490), Touches: 2, Source: SRLevelSwing},
			{Price: pts(22540), Touches: 3, Source: SRLevelSwing},
		},
	}
	st.Side, st.Kind = SideCall, SRKindPullback
	st.IndexEntry, st.StopLevel, st.TargetLevel, st.Risk = pts(22497), pts(22447), pts(22598), pts(50)
	st.EntryTS = srNow
	return s, st
}

func exitAt(s *Nifty50SR, st *nifty50SRState, close int64) *Signal {
	c := srCandle(close)
	st.LastClose = c.Close
	return s.evaluateExit(c, st)
}

func TestSRSupportStopHoldsDipIntoSupport(t *testing.T) {
	s, st := supportStopCall(t, "breakeven")
	if sig := exitAt(s, st, pts(22528)); sig != nil {
		t.Fatalf("exited on the run: %s", sig.Reason)
	}
	// Stop under the ×3 support, zone 10 pts; the ×2 level is ignored.
	if !st.Breakeven || st.StopLevel != pts(22473) || st.SupportLvl != pts(22483) {
		t.Fatalf("breakeven=%v stop=%d support=%d", st.Breakeven, st.StopLevel, st.SupportLvl)
	}
	// The Oct 13 dip: below entry, into the support zone, then back.
	for _, c := range []int64{pts(22491), pts(22485), pts(22476), pts(22510), pts(22560)} {
		if sig := exitAt(s, st, c); sig != nil {
			t.Fatalf("exited at %d: %s", c, sig.Reason)
		}
	}
	if st.Side != SideCall {
		t.Fatal("position closed")
	}
}

func TestSRSupportStopExitsWhenSupportBreaks(t *testing.T) {
	s, st := supportStopCall(t, "breakeven")
	exitAt(s, st, pts(22528))
	sig := exitAt(s, st, pts(22470))
	if sig == nil || !strings.Contains(sig.Reason, "support=") {
		t.Fatalf("no support-break exit: %+v", sig)
	}
}

func TestSRSupportStopConfirmNeedsTwoCloses(t *testing.T) {
	s, st := supportStopCall(t, "breakeven")
	s.cfg.SupportStopConfirm = 2
	exitAt(s, st, pts(22528))
	if sig := exitAt(s, st, pts(22470)); sig != nil {
		t.Fatalf("exited on the first close: %s", sig.Reason)
	}
	if sig := exitAt(s, st, pts(22480)); sig != nil || st.BreakBars != 0 {
		t.Fatalf("reclaim: sig=%v breakBars=%d", sig, st.BreakBars)
	}
	exitAt(s, st, pts(22470))
	if sig := exitAt(s, st, pts(22468)); sig == nil {
		t.Fatal("no exit after two closes beyond")
	}
}

func TestSRSupportStopNoStrongLevelKeepsStop(t *testing.T) {
	s, st := supportStopCall(t, "breakeven")
	st.ctx.state.Levels = []SRLevel{{Price: pts(22483), Touches: 2, Source: SRLevelSwing}}
	exitAt(s, st, pts(22528))
	if !st.Breakeven || st.StopLevel != pts(22447) || st.SupportLvl != 0 {
		t.Fatalf("breakeven=%v stop=%d support=%d", st.Breakeven, st.StopLevel, st.SupportLvl)
	}
}

func TestSRSupportStopZoneFromATR(t *testing.T) {
	s, st := supportStopCall(t, "breakeven")
	s.cfg.SupportStopZoneATRPct = 25 // 38 pts ATR → 9.5 pts
	exitAt(s, st, pts(22528))
	if want := pts(22483) - pts(38)*25/100; st.StopLevel != want {
		t.Fatalf("stop=%d want %d", st.StopLevel, want)
	}
}

func TestSRSupportStopTrailStepsUpNeverDown(t *testing.T) {
	s, st := supportStopCall(t, "trail")
	exitAt(s, st, pts(22528))
	if st.StopLevel != pts(22473) {
		t.Fatalf("stop=%d", st.StopLevel)
	}
	exitAt(s, st, pts(22560)) // above the 22540 ×3 level
	if st.StopLevel != pts(22530) || st.SupportLvl != pts(22540) {
		t.Fatalf("trail: stop=%d support=%d", st.StopLevel, st.SupportLvl)
	}
	exitAt(s, st, pts(22535)) // into the zone: the nearest level is 22483 again
	if st.StopLevel != pts(22530) {
		t.Fatalf("stop loosened to %d", st.StopLevel)
	}
}

func TestSRSupportStopPutMirrors(t *testing.T) {
	s, st := srTest(t)
	s.cfg.SupportStopMode, s.cfg.SupportStopZonePts = "breakeven", pts(10)
	st.ctx.state = SRState{Levels: []SRLevel{{Price: pts(22520), Touches: 3, Source: SRLevelSwing}}}
	st.Side, st.Kind = SidePut, SRKindPullback
	st.IndexEntry, st.StopLevel, st.TargetLevel, st.Risk = pts(22500), pts(22550), pts(22400), pts(50)
	st.EntryTS = srNow
	exitAt(s, st, pts(22470))
	if st.StopLevel != pts(22530) || st.SupportLvl != pts(22520) {
		t.Fatalf("stop=%d support=%d", st.StopLevel, st.SupportLvl)
	}
	if sig := exitAt(s, st, pts(22525)); sig != nil {
		t.Fatalf("exited inside the zone: %s", sig.Reason)
	}
	if sig := exitAt(s, st, pts(22531)); sig == nil {
		t.Fatal("no exit above the zone")
	}
}

func TestSRSupportStopOffKeepsEntryBreakeven(t *testing.T) {
	s, st := supportStopCall(t, "off")
	exitAt(s, st, pts(22528))
	if st.StopLevel != pts(22497) || st.SupportLvl != 0 {
		t.Fatalf("stop=%d support=%d", st.StopLevel, st.SupportLvl)
	}
	if sig := exitAt(s, st, pts(22491)); sig == nil {
		t.Fatal("off mode should stop at entry as before")
	}
}

func TestSRSupportStopSurvivesSnapshot(t *testing.T) {
	s, st := supportStopCall(t, "breakeven")
	s.cfg.SupportStopConfirm = 2
	exitAt(s, st, pts(22528))
	exitAt(s, st, pts(22470))
	got := restoreNifty50SR(s.cfg, snapshotNifty50SR("NSE:NIFTY", st))
	if got.SupportLvl != st.SupportLvl || got.BreakBars != 1 || got.StopLevel != st.StopLevel {
		t.Fatalf("restored support=%d breakBars=%d stop=%d", got.SupportLvl, got.BreakBars, got.StopLevel)
	}
}

func TestSRSupportStopDefaults(t *testing.T) {
	cfg := DefaultNifty50SRConfig()
	if cfg.SupportStopMode != "breakeven" || cfg.SupportStopTouches != 3 || cfg.SupportStopZonePts != pts(3) {
		t.Fatalf("mode=%s touches=%d zone=%d", cfg.SupportStopMode, cfg.SupportStopTouches, cfg.SupportStopZonePts)
	}
}
