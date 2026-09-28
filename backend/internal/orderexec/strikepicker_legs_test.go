package orderexec

import (
	"errors"
	"testing"
	"time"
)

func TestResolveStrike(t *testing.T) {
	sp := NewStrikePicker(nil)
	calls := 0
	sp.lookup = func(symbol string) (string, int64, error) {
		calls++
		if symbol == "NIFTY06OCT2624200CE" {
			return "9001", 75, nil
		}
		return "", 0, errors.New("not found")
	}
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istZone) // Wednesday → next Tuesday 06 Oct
	info, err := sp.ResolveStrike(now, 24200, "ce")
	if err != nil {
		t.Fatal(err)
	}
	if info.Token != "9001" || info.Symbol != "NIFTY06OCT2624200CE" || info.Strike != 24200 || info.LotSize != 75 {
		t.Fatalf("info = %+v", info)
	}
	if exp := sp.CurrentExpiry(); exp.Format("2006-01-02") != "2026-10-06" {
		t.Errorf("expiry = %v", exp)
	}
	n := calls
	if _, err := sp.ResolveStrike(now, 24200, "CE"); err != nil || calls != n {
		t.Errorf("second resolve should be cached (calls %d → %d, err %v)", n, calls, err)
	}
	if _, err := sp.ResolveStrike(now, 24225, "CE"); err == nil {
		t.Error("off-step strike must fail")
	}
	if _, err := sp.ResolveStrike(now, 24300, "CE"); err == nil {
		t.Error("unknown contract must fail")
	}
	if _, err := sp.ResolveStrike(now, 24200, "XX"); err == nil {
		t.Error("bad option type must fail")
	}
	sp.Reset()
	if !sp.CurrentExpiry().IsZero() {
		t.Error("Reset must clear expiry")
	}
	if got := sp.NextExpiry(now).Format("2006-01-02"); got != "2026-10-06" {
		t.Errorf("NextExpiry = %s", got)
	}
}
