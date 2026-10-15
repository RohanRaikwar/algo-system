package gateway

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestMergeTFCandles_DedupsSortsAndCaps(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 5, 4, m, 0, 0, time.UTC) }
	out := []CandleOut{{TS: at(3).Format(time.RFC3339)}, {TS: at(4).Format(time.RFC3339)}}
	extra := []model.TFCandle{{TS: at(1), Close: 100}, {TS: at(2), Close: 200}, {TS: at(3), Close: 999}}

	got := mergeTFCandles(out, extra, "NSE", "99926000", 60, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	want := []string{at(2).Format(time.RFC3339), at(3).Format(time.RFC3339), at(4).Format(time.RFC3339)}
	for i, w := range want {
		if got[i].TS != w {
			t.Fatalf("got[%d] = %s, want %s", i, got[i].TS, w)
		}
	}
	if got[1].Close == 999 {
		t.Fatalf("archive candle replaced an existing one")
	}
	if got[0].Close != 200 || got[0].Exchange != "NSE" || got[0].TF != 60 {
		t.Fatalf("archive candle fields wrong: %+v", got[0])
	}
}

func TestColdBeforeTS(t *testing.T) {
	oldest := time.Date(2026, 10, 5, 3, 45, 0, 0, time.UTC)
	if got := coldBeforeTS([]CandleOut{{TS: oldest.Format(time.RFC3339)}}, time.Time{}); got != oldest.Unix() {
		t.Fatalf("with candles: %d, want %d", got, oldest.Unix())
	}
	before := oldest.Add(500 * time.Millisecond)
	if got := coldBeforeTS(nil, before); got != oldest.Unix()+1 {
		t.Fatalf("with before: %d, want %d", got, oldest.Unix()+1)
	}
}
