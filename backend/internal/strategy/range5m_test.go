package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func range5mConfig() Nifty50RangeConfig {
	cfg := DefaultNifty50RangeConfig()
	cfg.IndexToken = ""
	cfg.MeanReversionEnabled = true
	return cfg // EntryTFMinutes = 5 (default)
}

// hammer5m returns five 1m bars that aggregate into hammerAt(sup).
func hammer5m(sup int64) []ohlcv {
	return []ohlcv{
		{Open: sup + 500, High: sup + 700, Low: sup + 200, Close: sup + 300},
		{Open: sup + 300, High: sup + 400, Low: sup - 2000, Close: sup - 1500},
		{Open: sup - 1500, High: sup - 200, Low: sup - 1800, Close: sup - 300},
		{Open: sup - 300, High: sup + 900, Low: sup - 400, Close: sup + 800},
		{Open: sup + 800, High: sup + 1100, Low: sup + 700, Close: sup + 1000},
	}
}

// feed1m feeds bars one minute apart from h:m and returns the signals by minute index.
func feed1m(s *Nifty50Range, f *rangeFeed, h, m int, bars []ohlcv) map[int]*Signal {
	out := map[int]*Signal{}
	for i, b := range bars {
		if sig := s.OnTFCandle(tfc(day4(f, h, m).Add(time.Duration(i)*time.Minute), b)); sig != nil {
			out[i] = sig
		}
	}
	return out
}

func TestRange5mDefaultIsFiveMinuteEntry(t *testing.T) {
	if DefaultNifty50RangeConfig().EntryTFMinutes != 5 {
		t.Fatal("default entry timeframe must be 5m")
	}
}

func TestRange5mEntryOnlyAtFiveMinuteClose(t *testing.T) {
	s, f := newWarmRange(t, range5mConfig())
	quiet(t, runUntil(s, f, 10, 25))
	rs, _ := s.RangeState("NSE:NIFTY")
	if !rs.InRange {
		t.Fatalf("not in range: %+v", rs)
	}
	sigs := feed1m(s, f, 10, 25, hammer5m(rs.Support))
	for i := 0; i < 4; i++ {
		if sigs[i] != nil {
			t.Fatalf("entry at minute %d, before the 5m bar closed: %+v", i, sigs[i])
		}
	}
	sig := sigs[4]
	if sig == nil || sig.Action != ActionBuy || sig.Side != SideCall || !strings.Contains(sig.Reason, RangeKindMeanReversion) {
		t.Fatalf("expected CALL at the 10:29 5m close, got %+v", sig)
	}
}

func TestRange5mIgnoresOneMinuteHammer(t *testing.T) {
	s, f := newWarmRange(t, range5mConfig())
	quiet(t, runUntil(s, f, 10, 27))
	rs, _ := s.RangeState("NSE:NIFTY")
	// A textbook 1m hammer mid-bucket (10:27) must not trigger a 5m strategy.
	if sig := s.OnTFCandle(tfc(day4(f, 10, 27), hammerAt(rs.Support))); sig != nil {
		t.Fatalf("1m hammer triggered a 5m entry: %+v", sig)
	}
}

func TestRange5mStopsStillCheckedEveryMinute(t *testing.T) {
	s, f := newWarmRange(t, range5mConfig())
	quiet(t, runUntil(s, f, 10, 25))
	rs, _ := s.RangeState("NSE:NIFTY")
	if sigs := feed1m(s, f, 10, 25, hammer5m(rs.Support)); sigs[4] == nil {
		t.Fatal("setup entry failed")
	}
	stop := s.instruments["NSE:NIFTY"].StopLevel
	// 10:31 is mid-bucket: a 1m close through the stop still exits.
	p := stop - 500
	ex := s.OnTFCandle(tfc(day4(f, 10, 31), ohlcv{Open: p + 300, High: p + 400, Low: p - 100, Close: p}))
	if ex == nil || ex.Action != ActionExit || ex.Side != SideCall || !strings.Contains(ex.Reason, "STOP") {
		t.Fatalf("expected 1m stop exit, got %+v", ex)
	}
}

// up5m returns five rising 1m bars from open to close (a strong bullish 5m bar).
func up5m(open, close int64) []ohlcv {
	out := make([]ohlcv, 5)
	step := (close - open) / 5
	p := open
	for i := range out {
		n := p + step
		if i == 4 {
			n = close
		}
		out[i] = ohlcv{Open: p, High: n + 100, Low: p - 100, Close: n}
		p = n
	}
	return out
}

func TestRange5mBreakoutNeedsNextFiveMinuteBar(t *testing.T) {
	cfg := range5mConfig()
	cfg.MeanReversionEnabled = false
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 11, 15))
	rs, _ := s.RangeState("NSE:NIFTY")
	edge := rs.Resistance
	if sigs := feed1m(s, f, 11, 15, up5m(edge-2000, edge+1500)); len(sigs) != 0 {
		t.Fatalf("first 5m breakout bar must only arm: %+v", sigs)
	}
	if st := s.instruments["NSE:NIFTY"]; st.PendingBreak != SideCall {
		t.Fatalf("breakout not armed: %s", st.PendingBreak)
	}
	sigs := feed1m(s, f, 11, 20, up5m(edge+1500, edge+1800))
	for i := 0; i < 4; i++ {
		if sigs[i] != nil {
			t.Fatalf("breakout entry mid-bucket at minute %d", i)
		}
	}
	if sig := sigs[4]; sig == nil || sig.Side != SideCall || !strings.Contains(sig.Reason, "BREAKOUT") {
		t.Fatalf("expected breakout CALL at the second 5m close, got %+v", sigs[4])
	}
}

func TestRangeStopBufferUsesATR(t *testing.T) {
	s := NewNifty50RangeWithConfig(1, range5mConfig())
	if got := s.stopBuffer(RangeState{ATR15: 2000}); got != 2500 {
		t.Fatalf("small ATR: buffer %d, want the 25-pt minimum", got)
	}
	if got := s.stopBuffer(RangeState{ATR15: 8000}); got != 4000 {
		t.Fatalf("ATR 80 pts: buffer %d, want 40 pts", got)
	}
}

func TestMinuteVolume(t *testing.T) {
	var m minuteVolume
	base := time.Date(2026, 1, 8, 4, 30, 0, 0, time.UTC)
	if _, _, ok := m.observe(base.Add(5*time.Second), 1000); ok {
		t.Fatal("first tick only initialises")
	}
	m.observe(base.Add(40*time.Second), 1500)
	minute, vol, ok := m.observe(base.Add(62*time.Second), 1600)
	if !ok || !minute.Equal(base) || vol != 500 {
		t.Fatalf("minute=%v vol=%d ok=%v", minute, vol, ok)
	}
	// Day counter reset (new session) re-initialises without a bogus minute.
	if _, _, ok := m.observe(base.Add(24*time.Hour), 10); ok {
		t.Fatal("counter reset must not emit")
	}
}

func TestFuturesVolumeFeedsRangeContext(t *testing.T) {
	cfg := range5mConfig()
	s := NewNifty50RangeWithConfig(1, cfg)
	s.SetVolumeToken("NFO:FUT")
	start := time.Date(2026, 1, 8, 10, 0, 0, 0, ist)
	// Warm one index bar so the instrument state exists.
	s.OnTFCandle(tfc(start.Add(-time.Minute), ohlcv{Open: 1, High: 1, Low: 1, Close: 1}))
	dayVol := int64(10000)
	for i := 0; i < 3; i++ {
		for sec := 0; sec < 60; sec += 20 {
			dayVol += 100
			s.OnTick(model.Tick{Token: "FUT", Exchange: "NFO", Price: 1, DayVolume: dayVol,
				EventTS: start.Add(time.Duration(i)*time.Minute + time.Duration(sec)*time.Second)})
		}
	}
	// Minute 10:01 finished when the 10:02 ticks began: 3 ticks × 100.
	s.OnTFCandle(tfc(start, ohlcv{Open: 1, High: 1, Low: 1, Close: 1}))
	s.OnTFCandle(tfc(start.Add(time.Minute), ohlcv{Open: 1, High: 1, Low: 1, Close: 1}))
	st := s.instruments["NSE:NIFTY"]
	n := len(st.ctx.bars1)
	if n < 2 || st.ctx.bars1[n-1].Volume != 300 {
		t.Fatalf("index bar volume from futures = %+v", st.ctx.bars1)
	}
}

func TestVolumeAtLeast5(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	for i := 0; i < 5; i++ {
		rc.on5m(tfBar{ohlcv: ohlcv{Volume: 1000}})
	}
	rc.on5m(tfBar{ohlcv: ohlcv{Volume: 1300}})
	if !rc.volumeAtLeast5(120, true) || rc.volumeAtLeast5(150, true) {
		t.Fatal("5m volume vs average")
	}
}
