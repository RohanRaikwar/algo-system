package instruments

import (
	"testing"
	"time"

	"trading-systemv1/internal/underlying"
)

// bankNiftyMaster lists BANKNIFTY NFO monthlies (last Tuesday) beside NIFTY
// weeklies on the same segment.
func bankNiftyMaster() *InstrumentMaster {
	im := &InstrumentMaster{}
	im.setInstrumentsLocked([]Instrument{
		{Token: "45123", Symbol: "BANKNIFTY29DEC2657000CE", Name: "BANKNIFTY", Expiry: "29DEC2026", Strike: "5700000.000000", LotSize: "30", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
		{Token: "45124", Symbol: "BANKNIFTY29DEC2657000PE", Name: "BANKNIFTY", Expiry: "29DEC2026", Strike: "5700000.000000", LotSize: "30", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
		{Token: "45200", Symbol: "BANKNIFTY29DEC2657100CE", Name: "BANKNIFTY", Expiry: "29DEC2026", Strike: "5710000.000000", LotSize: "30", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
		{Token: "46001", Symbol: "BANKNIFTY26JAN2757000CE", Name: "BANKNIFTY", Expiry: "26JAN2027", Strike: "5700000.000000", LotSize: "30", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
		{Token: "53408", Symbol: "NIFTY01DEC2626000CE", Name: "NIFTY", Expiry: "01DEC2026", Strike: "2600000.000000", LotSize: "65", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
	})
	im.loaded = true
	return im
}

func bankNiftyPicker() *StrikePicker {
	sp := NewStrikePickerFor(nil, underlying.Get("BANKNIFTY"))
	sp.master = bankNiftyMaster()
	return sp
}

func TestLastWeekdayOfMonth(t *testing.T) {
	for _, c := range []struct {
		y    int
		m    time.Month
		want time.Time
	}{
		{2026, time.November, istDay(2026, 11, 24)},
		{2026, time.December, istDay(2026, 12, 29)},
		{2026, time.December + 1, istDay(2027, 1, 26)},
		{2026, time.June, istDay(2026, 6, 30)}, // month ends on a Tuesday
	} {
		if got := lastWeekdayOfMonth(c.y, c.m, time.Tuesday); !got.Equal(c.want) {
			t.Errorf("lastWeekdayOfMonth(%d,%v) = %v, want %v", c.y, c.m, got, c.want)
		}
	}
}

func TestBankNiftyPicker_ListedMonthlies(t *testing.T) {
	sp := bankNiftyPicker()
	wed := time.Date(2026, 11, 25, 10, 0, 0, 0, istZone)
	if got := sp.candidateExpiries(wed); len(got) != 2 || got[0] != "29DEC26" || got[1] != "26JAN27" {
		t.Fatalf("candidateExpiries = %v", got)
	}
	if got := sp.automationExpiries(wed); len(got) != 2 || !got[0].Equal(istDay(2026, 12, 29)) {
		t.Fatalf("automationExpiries = %v", got)
	}
	got := sp.im().FindOption("BANKNIFTY", "NFO", istDay(2026, 12, 29), 57000, "CE")
	if got == nil || got.Token != "45123" {
		t.Fatalf("FindOption = %v", got)
	}
	// NIFTY's master rows on the same segment are not BANKNIFTY's.
	if sp.im().FindOption("BANKNIFTY", "NFO", istDay(2026, 12, 1), 26000, "CE") != nil {
		t.Fatal("NIFTY contract found as BANKNIFTY")
	}
	if step := sp.im().StrikeStep("BANKNIFTY", "NFO", istDay(2026, 11, 25)); step != 100 {
		t.Fatalf("StrikeStep = %d", step)
	}
}

// Without master expiries the guess is the month's last Tuesday, not every week's.
func TestBankNiftyPicker_MonthlyFallback(t *testing.T) {
	sp := bankNiftyPicker()
	sp.listedExpiries = func(time.Time) []time.Time { return nil }

	wed := time.Date(2026, 11, 25, 10, 0, 0, 0, istZone) // Nov expiry (24th) passed
	if got := sp.candidateExpiries(wed); got[0] != "29DEC26" {
		t.Fatalf("candidateExpiries = %v", got)
	}
	if got := sp.nextWeeklyExpiryDay(wed); !got.Equal(istDay(2026, 12, 29)) {
		t.Fatalf("next expiry = %v", got)
	}
	exp := sp.automationExpiries(wed)
	if len(exp) != 2 || !exp[0].Equal(istDay(2026, 12, 29)) || !exp[1].Equal(istDay(2027, 1, 26)) {
		t.Fatalf("automationExpiries = %v", exp)
	}

	// Expiry day itself: never bought by candidateExpiries; counts until 15:30.
	expDay := time.Date(2026, 11, 24, 10, 0, 0, 0, istZone)
	if got := sp.candidateExpiries(expDay); got[0] != "29DEC26" {
		t.Fatalf("expiry-day candidates = %v", got)
	}
	if got := sp.nextWeeklyExpiryDay(expDay); !got.Equal(istDay(2026, 11, 24)) {
		t.Fatalf("expiry day before close = %v", got)
	}
	after := time.Date(2026, 11, 24, 15, 31, 0, 0, istZone)
	if got := sp.nextWeeklyExpiryDay(after); !got.Equal(istDay(2026, 12, 29)) {
		t.Fatalf("expiry day after close = %v", got)
	}
	// Mid-month: this month's last Tuesday.
	mid := time.Date(2026, 11, 10, 10, 0, 0, 0, istZone)
	if got := sp.candidateExpiries(mid); got[0] != "24NOV26" {
		t.Fatalf("mid-month candidates = %v", got)
	}
}
