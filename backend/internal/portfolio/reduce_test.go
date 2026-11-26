package portfolio

import "testing"

func TestReducePosition(t *testing.T) {
	pf := New()
	pf.OpenPosition("1", "NFO", 195, 10000)
	pf.ReducePosition("1", "NFO", 130)
	if p := pf.GetPositions(); len(p) != 1 || p[0].Qty != 65 || p[0].AvgPrice != 10000 {
		t.Fatalf("after reduce %+v", p)
	}
	pf.ReducePosition("2", "NFO", 10) // unknown: no-op
	pf.ReducePosition("1", "NFO", 65)
	if p := pf.GetPositions(); len(p) != 0 {
		t.Fatalf("after selling all %+v", p)
	}
}
