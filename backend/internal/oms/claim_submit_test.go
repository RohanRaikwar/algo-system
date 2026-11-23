package oms

import (
	"testing"
	"time"
)

func TestLedger_ClaimAndSubmitOnce(t *testing.T) {
	for name, s := range map[string]Store{"journal": openTestJournal(t), "mem": &memStore{}} {
		t.Run(name, func(t *testing.T) {
			l := newTestLedger(t, s)
			ev := Event{ClientOrderID: "k1", Strategy: "TG", Side: "BUY", Qty: 75}
			ok, err := l.ClaimAndSubmit(ev)
			if err != nil || !ok {
				t.Fatalf("first ok=%v err=%v", ok, err)
			}
			if o, found := l.Order("k1"); !found || o.State != StateSubmitting {
				t.Fatalf("order %+v %v", o, found)
			}
			if ok, err := l.ClaimAndSubmit(ev); ok || err == nil && ok {
				t.Fatalf("replay claimed: ok=%v err=%v", ok, err)
			}
			if j, isJ := s.(*Journal); isJ {
				evs, err := j.Events(time.Time{})
				if err != nil || len(evs) != 1 {
					t.Fatalf("journal events %d err %v, want 1", len(evs), err)
				}
				// A fresh ledger on the same journal sees the claim.
				if ok, _ := newTestLedger(t, j).ClaimAndSubmit(Event{ClientOrderID: "k1"}); ok {
					t.Fatal("claim reusable after reopen")
				}
			}
		})
	}
}
