package strategy

import "testing"

func TestSRViewReportsRefusalsAndBlock(t *testing.T) {
	s, st := srTest(t)
	if _, ok := s.View("NSE:NIFTY"); ok {
		t.Fatal("view before the first candle")
	}
	st.LastCloseTS = srNow
	s.barTS = srNow
	s.reject("fade:not_at_level")
	s.reject("fade:not_at_level")
	s.reject("pullback:no_touch")

	v, ok := s.View("NSE:NIFTY")
	if !ok {
		t.Fatal("no view")
	}
	if v.LastReject != "pullback:no_touch" || !v.LastRejectTS.Equal(srNow) {
		t.Fatalf("last reject %q at %v", v.LastReject, v.LastRejectTS)
	}
	if v.Rejects["fade:not_at_level"] != 2 || v.Rejects["pullback:no_touch"] != 1 {
		t.Fatalf("rejects %v", v.Rejects)
	}
	if v.Side != "NONE" || v.Block != "" || v.Regime != "WARMING" {
		t.Fatalf("side %q block %q regime %q", v.Side, v.Block, v.Regime)
	}

	st.TradesToday = s.cfg.MaxTradesPerDay
	if v, _ := s.View("NSE:NIFTY"); v.Block != "trade_cap" {
		t.Fatalf("block %q, want trade_cap", v.Block)
	}
}

func TestSRViewRejectsResetEachDay(t *testing.T) {
	s, _ := srTest(t)
	c := srCandle(pts(22830))
	c.Exchange, c.Token = "NSE", "NIFTY"
	s.OnTFCandle(c)
	s.reject("fade:not_at_level")

	next := c
	next.TS = c.TS.AddDate(0, 0, 1)
	s.OnTFCandle(next)

	v, ok := s.View("NSE:NIFTY")
	if !ok {
		t.Fatal("no view")
	}
	if v.Rejects["fade:not_at_level"] != 0 {
		t.Fatalf("yesterday's rejects carried over: %v", v.Rejects)
	}
	if s.RejectStats()["fade:not_at_level"] != 1 {
		t.Fatalf("run total lost: %v", s.RejectStats())
	}
}
