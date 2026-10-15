package instruments

import (
	"testing"
	"time"
)

func istDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, istZone) }

func dayList(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.In(istZone).Format("02Jan2006")
	}
	return out
}

func sameDays(t *testing.T, got []time.Time, want ...string) {
	t.Helper()
	g := dayList(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("got %v, want %v", g, want)
		}
	}
}

// Oct 2026: Tue 20 Oct is a holiday, so that week expires Mon 19 Oct.
func oct2026Listed(time.Time) []time.Time {
	return []time.Time{istDay(2026, 10, 13), istDay(2026, 10, 19), istDay(2026, 10, 27)}
}

func TestAutomationExpiriesUsesListedHolidayShift(t *testing.T) {
	sp := NewStrikePicker(nil)
	sp.listedExpiries = oct2026Listed
	mon := time.Date(2026, 10, 12, 10, 0, 0, 0, istZone)
	sameDays(t, sp.automationExpiries(mon), "13Oct2026", "19Oct2026")

	tueAfterClose := time.Date(2026, 10, 13, 15, 31, 0, 0, istZone)
	sameDays(t, sp.automationExpiries(tueAfterClose), "19Oct2026", "27Oct2026")

	tueOpen := time.Date(2026, 10, 13, 11, 0, 0, 0, istZone)
	sameDays(t, sp.automationExpiries(tueOpen), "13Oct2026", "19Oct2026")
}

func TestAutomationExpiriesFallsBackToTuesdays(t *testing.T) {
	sp := NewStrikePicker(nil)
	sp.listedExpiries = func(time.Time) []time.Time { return nil }
	mon := time.Date(2026, 10, 12, 10, 0, 0, 0, istZone)
	sameDays(t, sp.automationExpiries(mon), "13Oct2026", "20Oct2026")
}

func TestOptionExpiriesFromMaster(t *testing.T) {
	if got := (&InstrumentMaster{}).OptionExpiries("NIFTY", istDay(2026, 10, 12)); got != nil {
		t.Fatalf("unloaded master: %v", got)
	}
	im := &InstrumentMaster{}
	im.setInstrumentsLocked([]Instrument{
		{Symbol: "NIFTY19OCT2625000CE", Name: "NIFTY", Expiry: "19OCT2026", ExchSeg: "NFO"},
		{Symbol: "NIFTY19OCT2625000PE", Name: "NIFTY", Expiry: "19OCT2026", ExchSeg: "NFO"},
		{Symbol: "NIFTY13OCT2625000CE", Name: "NIFTY", Expiry: "13OCT2026", ExchSeg: "NFO"},
		{Symbol: "NIFTY06OCT2625000CE", Name: "NIFTY", Expiry: "06OCT2026", ExchSeg: "NFO"}, // past
		{Symbol: "NIFTY27OCT26FUT", Name: "NIFTY", Expiry: "27OCT2026", ExchSeg: "NFO"},     // future, not an option
		{Symbol: "BANKNIFTY14OCT2650000CE", Name: "BANKNIFTY", Expiry: "14OCT2026", ExchSeg: "NFO"},
	})
	im.loaded = true
	sameDays(t, im.OptionExpiries("NIFTY", time.Date(2026, 10, 12, 9, 20, 0, 0, istZone)), "13Oct2026", "19Oct2026")
}
