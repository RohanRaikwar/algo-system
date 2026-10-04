package strategy

import "testing"

func TestSRPositionContexts(t *testing.T) {
	s := NewNifty50SR(65, DefaultNifty50SRConfig())
	if got := s.PositionContexts(); len(got) != 0 {
		t.Fatalf("flat strategy: want none, got %v", got)
	}
	st := s.getOrCreate("NSE:99926000")
	st.Side, st.IndexEntry, st.TargetLevel, st.StopLevel = SideCall, 2400000, 2404000, 2398000
	st.FNOToken, st.FNOEntryPrice = "NFO:43210", 10000

	got := s.PositionContexts()
	if len(got) != 1 {
		t.Fatalf("want 1 context, got %d", len(got))
	}
	c := got[0]
	if c.Strategy != "NIFTY50_SR" || c.Side != "CALL" || c.IndexToken != "99926000" ||
		c.IndexEntry != 2400000 || c.TargetLevel != 2404000 || c.StopLevel != 2398000 ||
		c.FNOToken != "43210" || c.FNOEntryPrice != 10000 {
		t.Fatalf("bad context: %+v", c)
	}
}

func TestRangePositionContextsFallsBackToCfgToken(t *testing.T) {
	cfg := DefaultNifty50RangeConfig()
	cfg.FNOPutToken = "NFO:555"
	s := NewNifty50RangeWithConfig(65, cfg)
	st := newNifty50RangeState(cfg)
	st.Side, st.IndexEntry, st.TargetLevel, st.StopLevel = SidePut, 2400000, 2396000, 2402000
	s.instruments["NSE:99926000"] = st

	got := s.PositionContexts()
	if len(got) != 1 || got[0].FNOToken != "555" || got[0].Side != "PUT" {
		t.Fatalf("bad contexts: %+v", got)
	}
}
