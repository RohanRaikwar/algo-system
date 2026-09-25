package indicator

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func tfc(i int, close int64) model.TFCandle {
	return model.TFCandle{Token: "T", Exchange: "NSE", TF: 60, TS: time.Unix(int64(i)*60, 0).UTC(),
		Open: close, High: close, Low: close, Close: close}
}

func value(t *testing.T, res []model.IndicatorResult, name string) float64 {
	t.Helper()
	for _, r := range res {
		if r.Name == name {
			return r.Value
		}
	}
	t.Fatalf("no %s in %+v", name, res)
	return 0
}

func TestProcess_ReplayedCandleNotAppliedTwice(t *testing.T) {
	e := NewEngine([]TFIndicatorConfig{{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 2}}}})
	e.Process(tfc(1, 100))
	res := e.Process(tfc(2, 200))
	if v := value(t, res, "SMA_2"); v != 150 {
		t.Fatalf("SMA_2 = %v, want 150", v)
	}
	if r := e.Process(tfc(2, 200)); r != nil {
		t.Fatalf("duplicate candle applied: %+v", r)
	}
	if r := e.Process(tfc(1, 100)); r != nil {
		t.Fatalf("older candle applied: %+v", r)
	}
	if !e.IsReplay(tfc(2, 0)) || e.IsReplay(tfc(3, 0)) {
		t.Fatal("IsReplay wrong")
	}
	if v := value(t, e.Process(tfc(3, 300)), "SMA_2"); v != 250 {
		t.Fatalf("SMA_2 after replays = %v, want 250 (replays must not have entered the window)", v)
	}
}

// A reload that adds an indicator warms only the new one from replayed
// history; the kept one skips what it has already seen.
func TestReload_BackfillWarmsOnlyNewIndicators(t *testing.T) {
	e := NewEngine([]TFIndicatorConfig{{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 2}}}})
	for i := 1; i <= 3; i++ {
		e.Process(tfc(i, int64(i*100)))
	}
	_, created := e.ReloadConfigs([]TFIndicatorConfig{{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 2}, {Type: "SMA", Period: 3}}}})
	if created == 0 {
		t.Fatal("reload should report a created indicator")
	}
	for i := 1; i <= 3; i++ { // reload backfill replays history
		for _, r := range e.Process(tfc(i, int64(i*100))) {
			if r.Name == "SMA_2" {
				t.Fatalf("kept SMA_2 re-applied replayed candle %d", i)
			}
		}
	}
	res := e.Process(tfc(4, 400))
	if v := value(t, res, "SMA_2"); v != 350 {
		t.Fatalf("SMA_2 = %v, want 350", v)
	}
	if v := value(t, res, "SMA_3"); v != 300 {
		t.Fatalf("SMA_3 = %v, want 300 (warmed from 200,300 + 400)", v)
	}
}

func TestSnapshotRestore_KeepsLastApplied(t *testing.T) {
	cfg := []TFIndicatorConfig{{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 2}}}}
	e := NewEngine(cfg)
	e.Process(tfc(1, 100))
	e.Process(tfc(2, 200))
	snap, err := SnapshotEngine(e, "x")
	if err != nil {
		t.Fatal(err)
	}
	r, err := RestoreEngine(cfg, snap)
	if err != nil {
		t.Fatal(err)
	}
	if res := r.Process(tfc(2, 200)); res != nil {
		t.Fatalf("restored engine re-applied candle it had seen: %+v", res)
	}
	if v := value(t, r.Process(tfc(3, 300)), "SMA_2"); v != 250 {
		t.Fatalf("SMA_2 = %v, want 250", v)
	}
}

type recentReader struct{ perToken int }

func (r *recentReader) ReadRecentTFCandles(tf, perToken int) ([]model.TFCandle, error) {
	r.perToken = perToken
	return nil, nil
}

func TestBackfillFromSQLite_AsksMaxPeriodPerToken(t *testing.T) {
	cfg := []TFIndicatorConfig{{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 20}, {Type: "EMA", Period: 50}}}}
	rr := &recentReader{}
	NewRestorer(cfg).BackfillFromSQLite(NewEngine(cfg), rr, nil)
	if rr.perToken != 50 {
		t.Fatalf("asked %d candles per token, want 50", rr.perToken)
	}
}
