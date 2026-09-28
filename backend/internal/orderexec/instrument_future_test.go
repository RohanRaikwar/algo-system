package orderexec

import (
	"testing"
	"time"
)

func TestNearestFuture(t *testing.T) {
	m := map[string]*Instrument{
		"NIFTY28OCT26FUT": {Token: "1", Symbol: "NIFTY28OCT26FUT", Name: "NIFTY", Expiry: "28OCT2026", InstrumentType: "FUTIDX", ExchSeg: "NFO"},
		"NIFTY25NOV26FUT": {Token: "2", Symbol: "NIFTY25NOV26FUT", Name: "NIFTY", Expiry: "25NOV2026", InstrumentType: "FUTIDX", ExchSeg: "NFO"},
		"NIFTY29SEP26FUT": {Token: "0", Symbol: "NIFTY29SEP26FUT", Name: "NIFTY", Expiry: "29SEP2026", InstrumentType: "FUTIDX", ExchSeg: "NFO"},
		"BANKNIFTY":       {Token: "9", Name: "BANKNIFTY", Expiry: "27OCT2026", InstrumentType: "FUTIDX", ExchSeg: "NFO"},
		"NIFTYOPT":        {Token: "8", Name: "NIFTY", Expiry: "06OCT2026", InstrumentType: "OPTIDX", ExchSeg: "NFO"},
	}
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istZone) // Sept series expired yesterday
	got, err := nearestFuture(m, "NIFTY", now)
	if err != nil || got.Token != "1" {
		t.Fatalf("nearest = %+v err=%v", got, err)
	}
	onExpiry := time.Date(2026, 10, 28, 10, 0, 0, 0, istZone)
	if got, _ := nearestFuture(m, "NIFTY", onExpiry); got.Token != "1" {
		t.Fatalf("on expiry day the current series still trades, got %+v", got)
	}
	if _, err := nearestFuture(m, "FINNIFTY", now); err == nil {
		t.Fatal("missing future must error")
	}
	if _, ok := parseMasterExpiry("28OCT2026"); !ok {
		t.Fatal("parse expiry")
	}
}
