package exitwatch

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// barTF builds a closed index candle of tf seconds; prices in points.
func barTF(tf int, ts time.Time, h, l int64) model.TFCandle {
	return model.TFCandle{
		Token: "99926000", Exchange: "NSE", TF: tf, TS: ts,
		Open: l * 100, High: h * 100, Low: l * 100, Close: h * 100,
	}
}

func model1m(ts time.Time, p int64) model.TFCandle { return barTF(60, ts, p, p) }

// preParams enables the pre-target rule and silences the score so only
// SR_REJECT_PRE (or nothing) can latch.
func preParams() Params {
	p := DefaultParams()
	p.PreRejectEnabled = true
	p.Bias = -100
	return p
}

// prePath drives a tracker every 250ms. closes maps an elapsed time to the
// favourable move of the 1m bar that closes then (bar start = el - 1m).
func prePath(t *testing.T, pos Position, p Params, book *LevelBook, dur time.Duration, path pathFn, closes map[time.Duration]int64) runResult {
	t.Helper()
	tr, err := NewTracker(pos, p.Compile())
	if err != nil {
		t.Fatal(err)
	}
	tr.SetATR(15 * pt) // tol 3pt, rejection 5pt
	tr.SetBook(book)
	sign := int64(1)
	if pos.Side == "PUT" {
		sign = -1
	}
	res := runResult{tr: tr}
	for el := time.Duration(0); el <= dur; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		if mv, ok := closes[el]; ok {
			tr.OnMinuteClose(pos.IndexEntry+sign*mv, ts.Add(-time.Minute))
		}
		mv := path(el)
		tr.OnIndexTick(pos.IndexEntry+sign*mv, ts)
		tr.OnOptionTick(pos.FNOEntryPrice+mv/2, 0, 0, 0, ts)
		a := tr.Evaluate(ts)
		res.as = append(res.as, a)
		res.events = append(res.events, tr.DrainEvents()...)
		if a.Latched {
			break
		}
	}
	return res
}

// up to +20 by 60s, back to +13 by 90s, then flat. Target is +40.
func touchAndFall(el time.Duration) int64 {
	s := el.Seconds()
	if s <= 60 {
		return lin(s, 0, 60, 0, 20)
	}
	return lin(s, 60, 90, 20, 13)
}

func sideSign(pos Position) int64 {
	if pos.Side == "PUT" {
		return -1
	}
	return 1
}

func strongBook(levels ...Level) *LevelBook {
	b := newBook()
	b.SetOurSR(levels)
	return b
}

func TestPreRejectExitsAtStrongLevelBeforeFarTarget(t *testing.T) {
	for _, pos := range []Position{callPos(), putPos()} {
		t.Run(pos.Side, func(t *testing.T) {
			sign := sideSign(pos)
			lvl := entryIdx + sign*20*pt
			book := strongBook(Level{Price: lvl, Type: "OUR_SR:SWING"})
			closes := map[time.Duration]int64{120 * time.Second: 13 * pt}
			r := prePath(t, pos, preParams(), book, 150*time.Second, touchAndFall, closes)
			a := last(r)
			if a.Decision != DecisionExit || a.ExitReason != ReasonPreReject {
				t.Fatalf("want EXIT %s, got %s %s (%s)", ReasonPreReject, a.Decision, a.ExitReason, a.ExitText)
			}
			if a.TS.Before(t0.Add(120 * time.Second)) {
				t.Fatalf("latched at %s before the confirming 1m close", a.TS.Sub(t0))
			}
			if !strings.Contains(a.ExitText, pts(lvl)) || !strings.Contains(a.ExitText, "OUR_SR:SWING") {
				t.Fatalf("exit text should name the level: %q", a.ExitText)
			}
		})
	}
}

func TestPreRejectNeedsConfirmingClose(t *testing.T) {
	book := strongBook(Level{Price: entryIdx + 20*pt, Type: "OUR_SR:SWING"})
	// The bar closes at +18: still inside the level's tolerance zone.
	closes := map[time.Duration]int64{120 * time.Second: 18 * pt}
	r := prePath(t, callPos(), preParams(), book, 150*time.Second, touchAndFall, closes)
	if a := last(r); a.Latched {
		t.Fatalf("no close back below the level: must not latch, got %s (%s)", a.ExitReason, a.ExitText)
	}

	p := preParams()
	p.PreRejectConfirmClose = false
	r = prePath(t, callPos(), p, book, 150*time.Second, touchAndFall, nil)
	if a := last(r); a.ExitReason != ReasonPreReject {
		t.Fatalf("confirm off: want %s on the pullback, got %s", ReasonPreReject, a.ExitReason)
	}
}

func TestPreRejectSkips(t *testing.T) {
	closes := map[time.Duration]int64{120 * time.Second: 13 * pt}
	cases := []struct {
		name  string
		p     func(*Params)
		level Level
		path  pathFn
	}{
		{name: "disabled", p: func(p *Params) { p.PreRejectEnabled = false },
			level: Level{Price: entryIdx + 20*pt, Type: "OUR_SR:SWING"}, path: touchAndFall},
		{name: "analyst level is not strong",
			level: Level{Price: entryIdx + 20*pt, Type: "ANALYST:R1"}, path: touchAndFall},
		{name: "1h swing not in default sources",
			level: Level{Price: entryIdx + 20*pt, Type: LevelSwingHigh1h}, path: touchAndFall},
		{name: "level too near target", // 40-32 = 8 < 0.3×40
			level: Level{Price: entryIdx + 32*pt, Type: "OUR_SR:SWING"},
			path: func(el time.Duration) int64 {
				s := el.Seconds()
				if s <= 60 {
					return lin(s, 0, 60, 0, 32)
				}
				return lin(s, 60, 90, 32, 25)
			}},
		{name: "move below minimum", // level at +8 < 10pt min move
			level: Level{Price: entryIdx + 8*pt, Type: "OUR_SR:SWING"},
			path: func(el time.Duration) int64 {
				s := el.Seconds()
				if s <= 60 {
					return lin(s, 0, 60, 0, 8)
				}
				return lin(s, 60, 90, 8, 1)
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := preParams()
			if c.p != nil {
				c.p(&p)
			}
			r := prePath(t, callPos(), p, strongBook(c.level), 150*time.Second, c.path, closes)
			if a := last(r); a.ExitReason == ReasonPreReject {
				t.Fatalf("must not fire: %s", a.ExitText)
			}
		})
	}
}

func TestPreRejectLaddersPastBrokenLevel(t *testing.T) {
	book := strongBook(
		Level{Price: entryIdx + 15*pt, Type: "OUR_SR:SWING"},
		Level{Price: entryIdx + 25*pt, Type: "OUR_SR:PREV_DAY"},
	)
	// Through +15 to +20, held 40s beyond it, up to +25, back to +18.
	path := func(el time.Duration) int64 {
		s := el.Seconds()
		switch {
		case s <= 30:
			return lin(s, 0, 30, 0, 20)
		case s <= 70:
			return 20 * pt
		case s <= 90:
			return lin(s, 70, 90, 20, 25)
		default:
			return lin(s, 90, 110, 25, 18)
		}
	}
	closes := map[time.Duration]int64{150 * time.Second: 18 * pt}
	r := prePath(t, callPos(), preParams(), book, 170*time.Second, path, closes)
	a := last(r)
	if a.ExitReason != ReasonPreReject || !strings.Contains(a.ExitText, pts(entryIdx+25*pt)) {
		t.Fatalf("want rejection at the second level, got %s (%s)", a.ExitReason, a.ExitText)
	}
}

func TestPreRejectStopsAtTarget(t *testing.T) {
	book := strongBook(Level{Price: entryIdx + 20*pt, Type: "OUR_SR:SWING"})
	// Straight to target: the runner decides, the pre-target rule is done.
	r := prePath(t, callPos(), preParams(), book, 60*time.Second, func(el time.Duration) int64 {
		return lin(el.Seconds(), 0, 30, 0, 45)
	}, nil)
	for _, a := range r.as {
		if a.ExitReason == ReasonPreReject {
			t.Fatalf("fired after a clean break to target: %s", a.ExitText)
		}
	}
}

func TestBookHTFSwingsAreStrongAndSurviveDayRoll(t *testing.T) {
	b := newBook()
	hour := func(d, hh int, h, l int64) {
		ts := time.Date(2026, 10, d, hh, 15, 0, 0, ist)
		b.OnHTFCandle(barTF(3600, ts, h, l))
	}
	// 1h swing high 24100 on Oct 5 at 11:15.
	hour(5, 9, 24020, 23990)
	hour(5, 10, 24050, 24000)
	hour(5, 11, 24100, 24040)
	hour(5, 12, 24060, 24010)
	hour(5, 13, 24030, 23980)
	// Next day: 1m bars roll the day; the 1h swing stays.
	b.OnCandle(model1m(time.Date(2026, 10, 6, 9, 15, 0, 0, ist), 24000))
	var got *Level
	for _, l := range b.Levels() {
		if l.Price == 2410000 {
			l := l
			got = &l
		}
	}
	if got == nil || got.Type != LevelSwingHigh1h || !got.Strong {
		t.Fatalf("1h swing high 24100 missing or not strong: %+v", b.Levels())
	}
	for _, l := range b.Levels() {
		if l.Type == LevelDayHigh && l.Strong {
			t.Fatalf("day high must not be strong: %+v", l)
		}
	}
}

func TestBookHTFLookbackTrims(t *testing.T) {
	p := DefaultParams()
	p.HTFLookbackDays = 1
	m := p.Compile()
	b := NewLevelBook(&m)
	ts := time.Date(2026, 10, 5, 9, 15, 0, 0, ist)
	for i := 0; i < 20; i++ {
		b.OnHTFCandle(barTF(3600, ts.Add(time.Duration(i)*time.Hour), 24000+int64(i%3)*10, 23990))
	}
	if n := len(b.htf[3600].bars); n != 7 {
		t.Fatalf("1 session of 1h bars = 7, kept %d", n)
	}
}

func TestClusterTagsMarkStrongMember(t *testing.T) {
	b := newBook()
	b.SetAnalyst([]Level{{Price: 2410000, Type: "ANALYST:R1"}})
	b.SetOurSR([]Level{{Price: 2410200, Type: "OUR_SR:SWING"}})
	ls := b.Levels()
	if len(ls) != 1 || !ls[0].Strong || ls[0].Type != "OUR_SR:SWING" || ls[0].Tags != "OUR_SR:SWING+ANALYST:R1" {
		t.Fatalf("merged cluster: %+v", ls)
	}
}

func TestPreRejectADXGate(t *testing.T) {
	closes := map[time.Duration]int64{120 * time.Second: 13 * pt}
	for _, c := range []struct {
		name   string
		max    float64
		adx    float64
		wantIt bool
	}{
		{"strong trend blocks", 35, 38, false},
		{"below max fires", 35, 30, true},
		{"unknown adx fires", 35, 0, true},
		{"gate off fires", 0, 50, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := preParams()
			p.PreRejectMaxADX = c.max
			book := strongBook(Level{Price: entryIdx + 20*pt, Type: "OUR_SR:SWING"})
			book.SetTrend(c.adx)
			r := prePath(t, callPos(), p, book, 150*time.Second, touchAndFall, closes)
			if got := last(r).ExitReason == ReasonPreReject; got != c.wantIt {
				t.Fatalf("fired=%v want %v (%s)", got, c.wantIt, last(r).ExitText)
			}
			if c.wantIt && c.adx > 0 && !strings.Contains(last(r).ExitText, "adx=") {
				t.Fatalf("exit text should carry adx: %q", last(r).ExitText)
			}
		})
	}
}

func TestPreRejectMinTouches(t *testing.T) {
	closes := map[time.Duration]int64{120 * time.Second: 13 * pt}
	for _, touches := range []int{1, 3} {
		p := preParams()
		p.PreRejectMinTouches = 2
		book := strongBook(OurSRLevel(entryIdx+20*pt, "SWING", touches))
		r := prePath(t, callPos(), p, book, 150*time.Second, touchAndFall, closes)
		if got, want := last(r).ExitReason == ReasonPreReject, touches >= 2; got != want {
			t.Fatalf("touches=%d: fired=%v want %v", touches, got, want)
		}
	}
}
