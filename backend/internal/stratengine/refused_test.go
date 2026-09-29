package stratengine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

func TestRecordRefusedEntryPublishesTodaysList(t *testing.T) {
	var sent []string
	svc := &Service{}
	svc.refusedPublishHook = func(p string) { sent = append(sent, p) }
	ctx := context.Background()
	sig := strategy.Signal{StrategyName: "NIFTY50_SR", Side: strategy.SidePut, Token: "99926000", Exchange: "NSE", Strike: 22600, Reason: "SR PULLBACK PUT"}

	day1 := time.Date(2026, 9, 29, 5, 16, 1, 0, time.UTC) // 10:46 IST
	svc.recordRefusedEntry(ctx, sig, "SR strike: no greeks", day1)
	svc.recordRefusedEntry(ctx, sig, "SR strike: no greeks", day1.Add(time.Minute))

	var v refusedView
	if err := json.Unmarshal([]byte(sent[len(sent)-1]), &v); err != nil {
		t.Fatal(err)
	}
	if v.Date != "2026-09-29" || len(v.Entries) != 2 {
		t.Fatalf("view = %+v, want 2 entries on 2026-09-29", v)
	}
	e := v.Entries[0]
	if e.Strategy != "NIFTY50_SR" || e.Side != "PUT" || e.Strike != 22600 || e.Reason != "SR strike: no greeks" ||
		e.StrategyReason != "SR PULLBACK PUT" || e.Token != "99926000" || e.Exchange != "NSE" {
		t.Fatalf("entry = %+v", e)
	}

	// A new IST day starts a fresh list.
	svc.recordRefusedEntry(ctx, sig, "market closed", day1.Add(24*time.Hour))
	if err := json.Unmarshal([]byte(sent[len(sent)-1]), &v); err != nil {
		t.Fatal(err)
	}
	if v.Date != "2026-09-30" || len(v.Entries) != 1 || v.Entries[0].Reason != "market closed" {
		t.Fatalf("next day view = %+v", v)
	}
}

func TestRefusedLogKeepsNewest(t *testing.T) {
	var l refusedLog
	now := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	var b []byte
	for i := 0; i < refusedMax+5; i++ {
		b, _ = l.add(RefusedEntry{Reason: string(rune('a' + i%26))}, now)
	}
	var v refusedView
	if err := json.Unmarshal(b, &v); err != nil || len(v.Entries) != refusedMax {
		t.Fatalf("entries = %d, want %d (%v)", len(v.Entries), refusedMax, err)
	}
	if v.Entries[0].Reason != string(rune('a'+5%26)) {
		t.Fatalf("oldest kept = %q, want the 6th entry", v.Entries[0].Reason)
	}
}
