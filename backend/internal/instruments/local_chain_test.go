package instruments

import (
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/optionmath"
)

// localPicker is sensexPicker with offline lookups answered by its master.
func localPicker() *StrikePicker {
	sp := sensexPicker()
	sp.lookup = func(symbol string) (string, int64, error) {
		inst, err := sp.master.GetInstrument(symbol)
		if err != nil {
			return "", 0, err
		}
		return inst.Token, 20, nil
	}
	return sp
}

func TestLocalOptionChain_GreeksFromLivePremiums(t *testing.T) {
	sp := localPicker()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, istZone) // Mon; listed 5 and 12 Nov
	const spot = 6463000                                 // 64630.00 in paise
	strikeOf := map[string]struct {
		k      float64
		call   bool
		expiry time.Time
	}{
		"1163095": {64600, true, istDay(2026, 11, 5)},
		"1163858": {64600, false, istDay(2026, 11, 5)},
		"1163965": {64700, true, istDay(2026, 11, 5)},
		"1170001": {64600, true, istDay(2026, 11, 12)},
	}
	quote := func(token string) (int64, int64) {
		s, ok := strikeOf[token]
		if !ok {
			return 0, 0
		}
		p := optionmath.Price(64630, s.k, optionmath.YearsTo(s.expiry, now), 0.15, 0.065, s.call)
		return int64(math.Round(p * 100)), 12000
	}

	chain, err := sp.LocalOptionChain(now, spot, 1, 6.5, quote)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 4 {
		t.Fatalf("chain has %d contracts, want 4: %+v", len(chain), chain)
	}
	for _, c := range chain {
		if math.Abs(c.IV-15) > 0.1 {
			t.Fatalf("%s IV = %v, want ~15", c.Symbol, c.IV)
		}
		if c.OptionType == OptionTypeCall && (c.Delta <= 0.3 || c.Delta >= 0.8) {
			t.Fatalf("%s call delta = %v", c.Symbol, c.Delta)
		}
		if c.OptionType == OptionTypePut && (c.Delta >= -0.2 || c.Delta <= -0.7) {
			t.Fatalf("%s put delta = %v", c.Symbol, c.Delta)
		}
		if c.Theta >= 0 || c.Gamma <= 0 || c.Premium <= 0 || c.LiquidityScore != 12000 || c.Token == "" {
			t.Fatalf("contract = %+v", c)
		}
	}
}

func TestLocalOptionChain_NoSpotOrNoPremiums(t *testing.T) {
	sp := localPicker()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, istZone)
	none := func(string) (int64, int64) { return 0, 0 }
	if _, err := sp.LocalOptionChain(now, 0, 1, 6.5, none); err == nil {
		t.Fatal("no spot accepted")
	}
	if _, err := sp.LocalOptionChain(now, 6463000, 1, 6.5, none); err == nil {
		t.Fatal("chain with no premiums accepted")
	}
}
