package strategy

import "testing"

func TestScalePoints(t *testing.T) {
	base := DefaultNifty50SRConfig()
	got := base.ScalePoints(func(p int64) int64 { return p * 33 / 10 })
	if got.TouchTolPts != 3300 || got.StopMinPts != 6600 || got.DayRunPts != 19800 ||
		got.SupportStopZonePts != 990 || got.MaxDayLossPts != 19800 {
		t.Fatalf("strategy points not scaled: %+v", got)
	}
	if got.Context.MinATRPts != 3960 || got.Context.Range.MinRangePts != 16500 || got.Context.Range.MaxRangePts != 99000 {
		t.Fatalf("context points not scaled: %+v", got.Context)
	}
	// Scale-free settings and the original stay untouched.
	if got.StopATRPct != base.StopATRPct || got.DayExtremePct != base.DayExtremePct || got.BoxATRPct != base.BoxATRPct {
		t.Fatalf("scale-free settings changed")
	}
	if base.TouchTolPts != 1000 || base.Context.Range.MinRangePts != 5000 {
		t.Fatalf("base config mutated")
	}
	if id := base.ScalePoints(func(p int64) int64 { return p }); id.TouchTolPts != base.TouchTolPts {
		t.Fatalf("identity scale changed values")
	}
}
