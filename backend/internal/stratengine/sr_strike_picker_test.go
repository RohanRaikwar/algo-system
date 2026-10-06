package stratengine

import (
	"errors"
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/optionpicker"
)

func TestNewPickerViewComparesWithOldPick(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 29, 42, 0, istSR)
	exp := time.Date(2026, 10, 13, 15, 30, 0, 0, istSR)
	side := &srSideView{Pick: &srContractView{Strike: 22850, Expiry: "2026-10-13"}}
	p := optionpicker.Pick{
		Contract: optionpicker.Contract{Token: "44625", Symbol: "NIFTY13OCT2622800PE", Strike: 22800, Option: "PE", Expiry: exp, IV: 11.2, Liquidity: 210000},
		Delta:    -0.49, DTE: 7, Score: 0.21,
		Quote: optionpicker.Quote{Bid: 17010, Ask: 17190, At: now.Add(-800 * time.Millisecond)},
	}
	newPickerView(side, p, optionpicker.Rejects{"delta": 12, "spread": 3}, nil, now)

	v := side.NewPick
	if v == nil || v.Strike != 22800 || v.Bid != 170.10 || v.Ask != 171.90 || v.Mid != 171.00 || v.Expiry != "2026-10-13" {
		t.Fatalf("new pick %+v", v)
	}
	if math.Abs(v.SpreadPct-1.0526) > 0.001 || math.Abs(v.QuoteAgeS-0.8) > 1e-9 {
		t.Errorf("spread %.4f%% age %.2fs", v.SpreadPct, v.QuoteAgeS)
	}
	if side.Agree == nil || *side.Agree {
		t.Errorf("22850 vs 22800 should disagree, got %v", side.Agree)
	}
	if side.NewRejects != "spread 3, delta 12" {
		t.Errorf("rejects %q", side.NewRejects)
	}
}

func TestNewPickerViewRefusalAndAgreement(t *testing.T) {
	now := time.Now()
	refused := &srSideView{}
	newPickerView(refused, optionpicker.Pick{}, optionpicker.Rejects{"quote stale": 25}, errors.New("no PE passes"), now)
	if refused.NewPick != nil || refused.NewError != "no PE passes" || refused.Agree != nil {
		t.Fatalf("refusal %+v", refused)
	}

	exp := time.Date(2026, 10, 13, 15, 30, 0, 0, istSR)
	same := &srSideView{Pick: &srContractView{Strike: 22800, Expiry: "2026-10-13"}}
	newPickerView(same, optionpicker.Pick{Contract: optionpicker.Contract{Strike: 22800, Expiry: exp},
		Quote: optionpicker.Quote{Bid: 100, Ask: 110, At: now}}, nil, nil, now)
	if same.Agree == nil || !*same.Agree {
		t.Fatalf("same strike and expiry should agree: %v", same.Agree)
	}
}
