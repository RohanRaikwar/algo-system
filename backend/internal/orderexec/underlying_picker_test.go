package orderexec

import (
	"testing"
	"time"

	"trading-systemv1/internal/underlying"
)

// sensexMaster lists SENSEX BFO weeklies the way Angel's master does:
// weekly symbols in YY+month-code+DD form, strikes in paise.
func sensexMaster() *InstrumentMaster {
	im := &InstrumentMaster{}
	im.setInstrumentsLocked([]Instrument{
		{Token: "1163095", Symbol: "SENSEX26N0564600CE", Name: "SENSEX", Expiry: "05NOV2026", Strike: "6460000.000000", LotSize: "20", InstrumentType: "OPTIDX", ExchSeg: "BFO"},
		{Token: "1163858", Symbol: "SENSEX26N0564600PE", Name: "SENSEX", Expiry: "05NOV2026", Strike: "6460000.000000", LotSize: "20", InstrumentType: "OPTIDX", ExchSeg: "BFO"},
		{Token: "1163965", Symbol: "SENSEX26N0564700CE", Name: "SENSEX", Expiry: "05NOV2026", Strike: "6470000.000000", LotSize: "20", InstrumentType: "OPTIDX", ExchSeg: "BFO"},
		{Token: "1170001", Symbol: "SENSEX26N1264600CE", Name: "SENSEX", Expiry: "12NOV2026", Strike: "6460000.000000", LotSize: "20", InstrumentType: "OPTIDX", ExchSeg: "BFO"},
		{Token: "864571", Symbol: "SENSEX26OCTFUT", Name: "SENSEX", Expiry: "29OCT2026", Strike: "0.000000", LotSize: "20", InstrumentType: "FUTIDX", ExchSeg: "BFO"},
		{Token: "53408", Symbol: "NIFTY03NOV2621250PE", Name: "NIFTY", Expiry: "03NOV2026", Strike: "2125000.000000", LotSize: "65", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
	})
	im.loaded = true
	return im
}

func sensexPicker() *StrikePicker {
	sp := NewStrikePickerFor(nil, underlying.Get("SENSEX"))
	sp.master = sensexMaster()
	return sp
}

func TestFindOption_BFOWeeklySymbol(t *testing.T) {
	im := sensexMaster()
	got := im.FindOption("SENSEX", "BFO", istDay(2026, 11, 5), 64600, "ce")
	if got == nil || got.Symbol != "SENSEX26N0564600CE" {
		t.Fatalf("FindOption = %v", got)
	}
	if im.FindOption("SENSEX", "NFO", istDay(2026, 11, 5), 64600, "CE") != nil {
		t.Fatalf("found SENSEX on NFO")
	}
	if step := im.StrikeStep("SENSEX", "BFO", istDay(2026, 11, 1)); step != 100 {
		t.Fatalf("StrikeStep = %d, want 100", step)
	}
	if fut, err := im.NearestFutureOn("SENSEX", "BFO", istDay(2026, 10, 20)); err != nil || fut.Token != "864571" {
		t.Fatalf("NearestFutureOn = %v, %v", fut, err)
	}
	if _, err := im.NearestFuture("SENSEX", istDay(2026, 10, 20)); err == nil {
		t.Fatalf("NFO NearestFuture found a BFO future")
	}
}

func TestSensexPicker_ResolvesFromMasterOnListedExpiry(t *testing.T) {
	sp := sensexPicker()
	sp.lookup = func(symbol string) (string, int64, error) {
		if symbol != "SENSEX26N0564600CE" {
			t.Fatalf("looked up %q", symbol)
		}
		return "1163095", 20, nil
	}
	// Mon 2 Nov: nearest listed expiry is Thu 5 Nov.
	info, err := sp.ResolveStrike(time.Date(2026, 11, 2, 10, 0, 0, 0, istZone), 64600, "CE")
	if err != nil {
		t.Fatal(err)
	}
	if info.Token != "1163095" || info.LotSize != 20 || info.Symbol != "SENSEX26N0564600CE" {
		t.Fatalf("info = %+v", info)
	}
	if sp.exchangeSeg() != "BFO" {
		t.Fatalf("exchange = %s", sp.exchangeSeg())
	}
	// A 50-point strike is not a SENSEX strike.
	if _, err := sp.ResolveStrike(time.Now(), 64650, "CE"); err == nil {
		t.Fatalf("64650 accepted with step 100")
	}
	// Unlisted contract: no guessed symbol.
	if _, ok := sp.LookupOn(istDay(2026, 11, 5), 99900, "CE"); ok {
		t.Fatalf("unlisted strike resolved")
	}
}

func TestSensexPicker_ExpiriesFromMasterThenThursday(t *testing.T) {
	sp := sensexPicker()
	mon := time.Date(2026, 11, 2, 10, 0, 0, 0, istZone)
	if got := sp.candidateExpiries(mon); len(got) != 2 || got[0] != "05NOV26" || got[1] != "12NOV26" {
		t.Fatalf("candidateExpiries = %v", got)
	}
	sp.listedExpiries = func(time.Time) []time.Time { return nil }
	if got := sp.candidateExpiries(mon); got[0] != "05NOV26" {
		t.Fatalf("Thursday fallback = %v", got)
	}
	if got := sp.nextWeeklyExpiryDay(mon); !got.Equal(istDay(2026, 11, 5)) {
		t.Fatalf("nextWeeklyExpiryDay = %v", got)
	}
}

// The NIFTY picker builds the same symbols and expiries as before profiles.
func TestNiftyPicker_Unchanged(t *testing.T) {
	sp := NewStrikePicker(nil)
	sp.master = &InstrumentMaster{} // not loaded: no master symbols
	if got := sp.optionSymbol(istDay(2026, 11, 3), 21250, "PE"); got != "NIFTY03NOV2621250PE" {
		t.Fatalf("NIFTY symbol = %q", got)
	}
	sp.master = sensexMaster()
	if got := sp.optionSymbol(istDay(2026, 11, 3), 21250, "PE"); got != "NIFTY03NOV2621250PE" {
		t.Fatalf("NIFTY master symbol = %q", got)
	}
	if sp.exchangeSeg() != "NFO" || sp.strikeStep != 50 || sp.prof().ExpiryWeekday != time.Tuesday {
		t.Fatalf("NIFTY picker changed: %s %d", sp.exchangeSeg(), sp.strikeStep)
	}
	mon := time.Date(2026, 11, 2, 10, 0, 0, 0, istZone)
	want := []string{"03NOV26", "04NOV26", "05NOV26", "10NOV26", "09NOV26", "11NOV26", "12NOV26"}
	got := sp.candidateExpiries(mon)
	if len(got) != len(want) {
		t.Fatalf("candidateExpiries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidateExpiries = %v, want %v", got, want)
		}
	}
}
