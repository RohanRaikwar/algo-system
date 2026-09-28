package strategy

import "testing"

func TestRangeViewReflectsState(t *testing.T) {
	s, f := newWarmRange(t, rangeTestConfig())
	if _, ok := s.View("NSE:NOPE"); ok {
		t.Fatal("unknown key must report no view")
	}
	quiet(t, runUntil(s, f, 10, 29))
	v, ok := s.View("NSE:NIFTY")
	if !ok || v.Regime != "RANGE" || v.Support == 0 || v.Resistance <= v.Support || v.Side != string(SideNone) {
		t.Fatalf("view before entry = %+v", v)
	}
	rs, _ := s.RangeState("NSE:NIFTY")
	s.OnTFCandle(tfc(day4(f, 10, 29), hammerAt(rs.Support)))
	v, _ = s.View("NSE:NIFTY")
	if v.Side != string(SideCall) || v.Kind != RangeKindMeanReversion || v.Stop == 0 || v.Target == 0 || v.Strike == 0 {
		t.Fatalf("view with position = %+v", v)
	}
	if v.MaxADX != 30 || v.TimeExitMin != hhmm(15, 0) || v.MaxTrades != 4 {
		t.Fatalf("config fields = %+v", v)
	}
}
