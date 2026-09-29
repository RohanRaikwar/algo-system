package stratengine

// fnoSubscribeMode is the seam behind M1: the ATM pair subscribe (and every
// other publishFNOSubscription caller) must use SnapQuote once the global
// option picker is running, since every option then needs a two-sided
// quote, not just Quote mode's LTP.

import (
	"testing"

	"trading-systemv1/internal/optionpicker"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

func TestFNOSubscribeModeFollowsPicker(t *testing.T) {
	svc := &Service{}
	if got := svc.fnoSubscribeMode(); got != smartconnect.ModeQuote {
		t.Fatalf("no picker: mode = %d, want Quote (%d)", got, smartconnect.ModeQuote)
	}

	svc.picker = optionpicker.New(optionpicker.Config{}, staticChain(nil), allResolver{}, &nopSub{})
	if got := svc.fnoSubscribeMode(); got != smartSnapQuote {
		t.Fatalf("picker running: mode = %d, want SnapQuote (%d)", got, smartSnapQuote)
	}
}
