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

func TestResolveStrikeOnUsesGivenExpiryAndKeepsCurrentExpiry(t *testing.T) {
	sp := NewStrikePicker(nil)
	sp.lookup = func(symbol string) (string, int64, error) {
		if symbol == "NIFTY13OCT2622700PE" {
			return "7001", 65, nil
		}
		return "", 0, errors.New("not found")
	}
	exp := time.Date(2026, 10, 13, 0, 0, 0, 0, istZone)
	info, err := sp.ResolveStrikeOn(exp, 22700, "pe")
	if err != nil || info.Token != "7001" || info.Symbol != "NIFTY13OCT2622700PE" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if !sp.CurrentExpiry().IsZero() {
		t.Fatalf("CurrentExpiry changed to %v", sp.CurrentExpiry())
	}
	if _, err := sp.ResolveStrikeOn(exp, 22725, "PE"); err == nil {
		t.Fatal("off-step strike must fail")
	}
}

func TestLookupOnNeverSearches(t *testing.T) {
	sp := NewStrikePicker(nil)
	sp.lookup = func(symbol string) (string, int64, error) {
		if symbol == "NIFTY13OCT2622700CE" {
			return "7002", 65, nil
		}
		return "", 0, errors.New("not in master")
	}
	exp := time.Date(2026, 10, 13, 0, 0, 0, 0, istZone)
	if info, ok := sp.LookupOn(exp, 22700, "CE"); !ok || info.Token != "7002" {
		t.Fatalf("info=%+v ok=%v", info, ok)
	}
	if _, ok := sp.LookupOn(exp, 22750, "CE"); ok {
		t.Fatal("miss reported as found")
	}
}
