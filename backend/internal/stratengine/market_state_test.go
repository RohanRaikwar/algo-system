package stratengine

import "testing"

// onMarketState is the small, Redis-free unit under test for the
// pub:market:state handler: on "open" (mdengine publishes this on every WS
// connect, not only the 09:15 open) it must reset the global picker's
// session and the old strike ladder so both re-subscribe their full
// contract list, since a fresh socket forgets mdengine's dynamic
// subscriptions. Any other payload must be a no-op.
func TestOnMarketStateResetsPickerAndLadderOnOpen(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.picker = testPicker(t, svc) // already warmed up: has streamed contracts
	if svc.picker.Status(pickerNow).Streamed == 0 {
		t.Fatal("test setup: picker not warmed up")
	}
	svc.ladderSubscribed = map[string]bool{"X": true}
	svc.ladderCenter.Store(2270000)

	svc.onMarketState("closed")
	if svc.picker.Status(pickerNow).Streamed == 0 {
		t.Fatal(`"closed" must not reset the picker session`)
	}
	if svc.ladderCenter.Load() == 0 {
		t.Fatal(`"closed" must not reset the ladder`)
	}
	if len(svc.ladderSubscribed) == 0 {
		t.Fatal(`"closed" must not clear ladderSubscribed`)
	}

	svc.onMarketState("open")
	if got := svc.picker.Status(pickerNow).Streamed; got != 0 {
		t.Fatalf("picker session not reset on market open: streamed=%d", got)
	}
	if svc.ladderCenter.Load() != 0 {
		t.Fatalf("ladderCenter = %d, want reset to 0", svc.ladderCenter.Load())
	}
	if len(svc.ladderSubscribed) != 0 {
		t.Fatalf("ladderSubscribed not cleared: %v", svc.ladderSubscribed)
	}

	// No picker (mode off / no consumers) must not panic.
	svc.picker = nil
	svc.onMarketState("open")
}
