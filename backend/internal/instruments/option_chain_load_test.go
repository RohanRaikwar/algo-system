package instruments

import (
	"errors"
	"testing"
	"time"

	"trading-systemv1/pkg/smartconnect"
)

func greekRow(strike float64, opt string) map[string]interface{} {
	return map[string]interface{}{"strikePrice": strike, "optionType": opt, "delta": 0.5}
}

func chainTestPicker(listed []time.Time) *StrikePicker {
	sp := NewStrikePicker(smartconnect.NewSmartConnect(smartconnect.Config{}))
	sp.listedExpiries = func(time.Time) []time.Time { return listed }
	return sp
}

func TestLoadOptionChainSkipsTodaysExpiry(t *testing.T) {
	sp := chainTestPicker([]time.Time{istDay(2026, 11, 3), istDay(2026, 11, 10)})
	var asked []string
	sp.greekCall = func(p map[string]any) (map[string]any, error) {
		asked = append(asked, p["expirydate"].(string))
		return map[string]any{"status": true, "data": []interface{}{greekRow(22700, "CE")}}, nil
	}
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, istZone) // expiry day
	chain, err := sp.LoadOptionChain(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "10NOV2026" {
		t.Fatalf("asked %v, want only 10NOV2026", asked)
	}
	if len(chain) != 1 || !chain[0].Expiry.Equal(istDay(2026, 11, 10)) {
		t.Fatalf("chain = %+v", chain)
	}
}

func TestLoadOptionChainKeepsExpiriesThatLoaded(t *testing.T) {
	sp := chainTestPicker([]time.Time{istDay(2026, 11, 10), istDay(2026, 11, 17)})
	sp.greekCall = func(p map[string]any) (map[string]any, error) {
		if p["expirydate"] == "10NOV2026" {
			return map[string]any{"status": false, "message": "No Data Available"}, nil
		}
		return map[string]any{"status": true, "data": []interface{}{greekRow(22700, "PE")}}, nil
	}
	chain, err := sp.LoadOptionChain(time.Date(2026, 11, 4, 10, 0, 0, 0, istZone))
	if err != nil || len(chain) != 1 || !chain[0].Expiry.Equal(istDay(2026, 11, 17)) {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}

	sp.greekCall = func(map[string]any) (map[string]any, error) { return nil, errors.New("down") }
	if _, err := sp.LoadOptionChain(time.Date(2026, 11, 4, 10, 0, 0, 0, istZone)); err == nil {
		t.Fatal("all expiries failing must return an error")
	}
}

func TestLoadOptionChainRefreshesSessionOnInvalidToken(t *testing.T) {
	sp := chainTestPicker([]time.Time{istDay(2026, 11, 10), istDay(2026, 11, 17)})
	refreshed := 0
	calls := 0
	sp.greekCall = func(map[string]any) (map[string]any, error) {
		calls++
		if refreshed == 0 {
			return map[string]any{"message": "Invalid Token"}, nil
		}
		return map[string]any{"status": true, "data": []interface{}{greekRow(22700, "CE")}}, nil
	}
	sp.SetSessionRefresher(func() error { refreshed++; return nil })
	chain, err := sp.LoadOptionChain(time.Date(2026, 11, 4, 10, 0, 0, 0, istZone))
	if err != nil || len(chain) != 2 {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
	if refreshed != 1 || calls != 3 {
		t.Fatalf("refreshed=%d calls=%d, want 1 refresh and one retry", refreshed, calls)
	}
}

func TestLoadOptionChainInvalidTokenWithoutRefresherFails(t *testing.T) {
	sp := chainTestPicker([]time.Time{istDay(2026, 11, 10)})
	sp.greekCall = func(map[string]any) (map[string]any, error) {
		return map[string]any{"message": "Invalid Token"}, nil
	}
	if _, err := sp.LoadOptionChain(time.Date(2026, 11, 4, 10, 0, 0, 0, istZone)); err == nil {
		t.Fatal("want error")
	}
}
