package strategy

import "testing"

// srLevelsFixture puts 15m swing highs at the given prices (window 2) and a
// previous-day high/low into a fresh SR context.
func srLevelsFixture(swingHighs []int64, pdHigh, pdLow int64) *srContext {
	sc := newSRContext(DefaultSRContextConfig())
	base := pts(22700)
	var bars []tfBar
	flat := func() {
		bars = append(bars, tfBar{ohlcv: ohlcv{High: base + pts(10), Low: base}})
	}
	flat()
	flat()
	for _, h := range swingHighs {
		bars = append(bars, tfBar{ohlcv: ohlcv{High: h, Low: base + pts(5)}})
		flat()
		flat()
	}
	sc.rc.bars15 = bars
	sc.rc.prevDayHigh, sc.rc.prevDayLow = pdHigh, pdLow
	return sc
}

func levelAt(levels []SRLevel, price, tol int64) (SRLevel, bool) {
	for _, l := range levels {
		if absInt64(l.Price-price) <= tol {
			return l, true
		}
	}
	return SRLevel{}, false
}

// The previous-day high used to be counted twice: once inside the swing
// clusters (UsePrevDayLevels) and again by levels().
func TestSRLevelsCountPrevDayOnce(t *testing.T) {
	sc := srLevelsFixture([]int64{pts(22900), pts(22902)}, pts(22905), pts(22500))
	l, ok := levelAt(sc.levels(), pts(22902), pts(15))
	if !ok {
		t.Fatalf("no level near 22,902: %+v", sc.levels())
	}
	if l.Touches != 3 {
		t.Errorf("touches=%d, want 3 (two swings + previous-day high once)", l.Touches)
	}
}

func TestSRLevelsPrevDayJoinsLoneSwing(t *testing.T) {
	// One swing at the prev-day high: two touches, not three.
	sc := srLevelsFixture([]int64{pts(22900)}, pts(22904), pts(22500))
	l, ok := levelAt(sc.levels(), pts(22904), pts(15))
	if !ok {
		t.Fatalf("no level near 22,904: %+v", sc.levels())
	}
	if l.Touches != 2 {
		t.Errorf("touches=%d, want 2", l.Touches)
	}
	lo, ok := levelAt(sc.levels(), pts(22500), 0)
	if !ok || lo.Touches != 1 || lo.Source != SRLevelPrevDay {
		t.Errorf("prev-day low level = %+v ok=%v", lo, ok)
	}
}

func TestSRStateCarriesPrevDay(t *testing.T) {
	sc := srLevelsFixture(nil, pts(22905), pts(22500))
	sc.refresh(pts(22700))
	st := sc.State()
	if st.PrevDayHigh != pts(22905) || st.PrevDayLow != pts(22500) {
		t.Errorf("prev day in state = %d/%d", st.PrevDayHigh, st.PrevDayLow)
	}
}
