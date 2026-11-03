package stratengine

import (
	"context"
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"
)

// minDTEFake lists every strike on one expiry, minDTE days out or more.
type minDTEFake struct {
	symbolResolver
	asked int // minDTE of the last lookup
}

func (f *minDTEFake) LookupNearestMinDTE(_ time.Time, minDTE int, strike int64, opt string) (instruments.StrikeInfo, bool) {
	f.asked = minDTE
	return instruments.StrikeInfo{Token: opt + itoa(strike), Symbol: "NIFTY10NOV26" + itoa(strike) + opt, Strike: strike}, true
}

// A broker error on the chain (OptionGreek "Invalid Token") makes the SR
// entry buy the strategy's ATM strike, SRMinDTE days out, not refuse it.
func TestSREntryNoGreeksTakesATMMinDTE(t *testing.T) {
	g := &fakeGreeks{err: errors.New(`OptionGreek(03Nov2026): message="Invalid Token"`)}
	svc := deltaSvc(g, 2400000, "CE24000")
	f := &minDTEFake{}
	svc.legResolver = f
	svc.cfg.SRMinDTE = 2
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())

	sig := buyCall(24000)
	sig.StrategyName = "NIFTY50_SR"
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, istSR)
	if err := svc.resolveEntryStrike(context.Background(), &sig, now); err != nil {
		t.Fatalf("want ATM entry, got %v", err)
	}
	if sig.FNOToken != "CE24000" || sig.FNOSymbol != "NIFTY10NOV2624000CE" || f.asked != 2 {
		t.Fatalf("bought %s %s minDTE=%d", sig.FNOToken, sig.FNOSymbol, f.asked)
	}
}

// Without a premium the ATM fallback still refuses.
func TestSREntryNoGreeksNoPremiumRefuses(t *testing.T) {
	g := &fakeGreeks{err: errors.New("down")}
	svc := deltaSvc(g, 2400000) // no option LTP
	svc.legResolver = &minDTEFake{}
	svc.subscribeHook = func([]string) {}
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())
	sig := buyCall(24000)
	sig.StrategyName = "NIFTY50_SR"
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("no premium must refuse")
	}
}

// Other strategies keep refusing without greeks.
func TestNonSREntryNoGreeksStillRefuses(t *testing.T) {
	g := &fakeGreeks{err: errors.New("down")}
	svc := deltaSvc(g, 2400000, "CE24000")
	svc.legResolver = &minDTEFake{}
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("non-SR entry without greeks must refuse")
	}
}

// One login per client per minute, whatever the number of callers.
func TestRefreshGreekSessionRateLimited(t *testing.T) {
	svc := &Service{}
	logins := 0
	svc.greekLogin = func(*smartconnect.SmartConnect) error { logins++; return nil }
	a := smartconnect.NewSmartConnect(smartconnect.Config{})
	b := smartconnect.NewSmartConnect(smartconnect.Config{})
	for i := 0; i < 3; i++ {
		if err := svc.refreshGreekSession(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.refreshGreekSession(b); err != nil {
		t.Fatal(err)
	}
	if logins != 2 {
		t.Fatalf("logins = %d, want 1 per client", logins)
	}

	failing := &Service{greekLogin: func(*smartconnect.SmartConnect) error { return errors.New("blocked") }}
	if err := failing.refreshGreekSession(a); err == nil {
		t.Fatal("want login error")
	}
	if err := failing.refreshGreekSession(a); err == nil {
		t.Fatal("throttled call must repeat the last error")
	}
}
