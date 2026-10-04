package exitwatch

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// bar builds a closed 1m candle at IST hh:mm on the test day; prices in points.
func bar(hh, mm int, o, h, l, c int64) model.TFCandle {
	return model.TFCandle{
		Token: "99926000", Exchange: "NSE", TF: 60,
		TS:   time.Date(2026, 10, 5, hh, mm, 0, 0, ist),
		Open: o * 100, High: h * 100, Low: l * 100, Close: c * 100,
	}
}

func newBook() *LevelBook {
	m := DefaultParams().Compile()
	return NewLevelBook(&m)
}

func hasLevel(ls []Level, price int64, typ string) bool {
	for _, l := range ls {
		if l.Price == price && l.Type == typ {
			return true
		}
	}
	return false
}

func TestBookORBAndFractals(t *testing.T) {
	b := newBook()
	// 09:15-09:29 opening range 24000-24030
	for m := 0; m < 15; m++ {
		if m == 14 && b.orbDone {
			t.Fatal("ORB must not complete before its last bar")
		}
		b.OnCandle(bar(9, 15+m, 24010, 24015+int64(m%3)*5, 24005-int64(m%2)*5, 24010))
	}
	if !b.orbDone {
		t.Fatal("ORB must complete on the 09:29 bar")
	}
	// swing high at 09:32 (24080), swing low at 09:36 (23990)
	seq := []int64{24040, 24060, 24080, 24060, 24040, 24020, 23990, 24020, 24040}
	for i, p := range seq {
		b.OnCandle(bar(9, 30+i, p, p+0, p-0, p))
	}
	ls := b.Levels()
	// ORB low 24000 has no stronger neighbour, so it keeps its own label;
	// ORB high 24025 coincides with an opening-range swing, which outranks it.
	if !hasLevel(ls, 2400000, LevelORBLow) || !hasLevel(ls, 2402500, LevelSwingHigh) {
		t.Fatalf("ORB levels wrong: %+v", ls)
	}
	if !b.orbDone || b.orbHigh != 2402500 || b.orbLow != 2400000 {
		t.Fatalf("orb high=%d low=%d done=%v", b.orbHigh, b.orbLow, b.orbDone)
	}
	if !hasLevel(ls, 2408000, LevelSwingHigh) {
		t.Fatalf("swing high 24080 missing: %+v", ls)
	}
	if !hasLevel(ls, 2399000, LevelSwingLow) {
		t.Fatalf("swing low 23990 missing: %+v", ls)
	}
}

func TestBookMergePrefersOurSRAndKeepsOldestSince(t *testing.T) {
	b := newBook()
	early := time.Date(2026, 10, 5, 9, 40, 0, 0, ist)
	b.SetAnalyst([]Level{{Price: 2410000, Type: "ANALYST:HORIZONTAL", Since: early}})
	b.SetOurSR([]Level{{Price: 2410300, Type: "OUR_SR:swing", Since: early.Add(time.Hour)}})
	ls := b.Levels()
	if len(ls) != 1 || ls[0].Type != "OUR_SR:swing" || !ls[0].Since.Equal(early) {
		t.Fatalf("merge wrong: %+v", ls)
	}
}

func TestBookVersionOnlyOnChange(t *testing.T) {
	b := newBook()
	b.SetOurSR([]Level{{Price: 2410000, Type: "OUR_SR:swing"}})
	v := b.Version()
	b.SetOurSR([]Level{{Price: 2410000, Type: "OUR_SR:swing"}})
	if b.Version() != v {
		t.Fatal("same levels bumped version")
	}
	b.SetOurSR([]Level{{Price: 2410000, Type: "OUR_SR:swing"}, {Price: 2420000, Type: "OUR_SR:pdh"}})
	if b.Version() == v {
		t.Fatal("new level did not bump version")
	}
}

func TestBookDayRollResets(t *testing.T) {
	b := newBook()
	b.OnTick(2400000, t0)
	b.OnCandle(bar(9, 15, 24000, 24010, 23990, 24000))
	b.OnTick(2500000, t0.Add(24*time.Hour))
	if b.dayLow != 2500000 || len(b.Bars()) != 0 || b.orbHigh != 0 {
		t.Fatalf("day not reset: low=%d bars=%d", b.dayLow, len(b.Bars()))
	}
}

func TestNextLevelBothSides(t *testing.T) {
	ls := []Level{{Price: 100}, {Price: 200}, {Price: 300}}
	if l, ok := nextLevel(ls, 195, 1, 10); !ok || l.Price != 300 {
		t.Fatalf("CALL within tol of 200 should skip to 300, got %v %v", l, ok)
	}
	if l, ok := nextLevel(ls, 150, 1, 10); !ok || l.Price != 200 {
		t.Fatalf("CALL next=%v", l)
	}
	if l, ok := nextLevel(ls, 250, -1, 10); !ok || l.Price != 200 {
		t.Fatalf("PUT next=%v", l)
	}
	// broken resistance acts as support: price above 200 going down finds 200
	if l, ok := nextLevel(ls, 230, -1, 10); !ok || l.Price != 200 {
		t.Fatalf("broken R as S: %v", l)
	}
	if _, ok := nextLevel(ls, 400, 1, 10); ok {
		t.Fatal("no level above 400")
	}
}

func TestBookDatesNewLevelsByTickClock(t *testing.T) {
	b := newBook()
	b.OnCandle(bar(9, 59, 24000, 24010, 23990, 24000)) // closes 10:00
	tick := t0.Add(90 * time.Second)                   // 10:01:30
	b.OnTick(2400500, tick)
	b.SetOurSR([]Level{{Price: 2405000, Type: "OUR_SR:swing"}})
	for _, l := range b.Levels() {
		if l.Price == 2405000 && !l.Since.Equal(tick) {
			t.Fatalf("new level dated %v, want tick clock %v", l.Since, tick)
		}
	}
}
