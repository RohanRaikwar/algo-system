package stratengine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// gatedGreeks blocks LoadOptionChain until gate is closed, like a slow
// OptionGreek call.
type gatedGreeks struct {
	chain   []orderexec.OptionContract
	started chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func (g *gatedGreeks) LoadOptionChain(time.Time) ([]orderexec.OptionContract, error) {
	g.once.Do(func() { close(g.started) })
	<-g.gate
	return g.chain, nil
}

// An entry must not wait for a chain load in progress: the signal path reads
// the cache only.
func TestEntryChainDoesNotWaitOnLoad(t *testing.T) {
	g := &gatedGreeks{chain: []orderexec.OptionContract{contract(24000, "CE", 0.5)},
		started: make(chan struct{}), gate: make(chan struct{})}
	svc := &Service{legResolver: symbolResolver{}}
	svc.greeks = g
	now := time.Now()

	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		if _, err := svc.optionChain(now); err != nil {
			t.Errorf("load: %v", err)
		}
	}()
	<-g.started

	done := make(chan error, 1)
	go func() { _, err := svc.entryChain(now); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cold cache must refuse the entry")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("entryChain blocked on a chain load in progress")
	}

	close(g.gate)
	<-loaded
	if chain, err := svc.entryChain(now); err != nil || len(chain) != 1 {
		t.Fatalf("after load: chain=%d err=%v", len(chain), err)
	}
}

// A cold cache refuses the entry, starts one background refresh, and the
// next signal finds the chain.
func TestEntryColdChainRefusesThenRefreshes(t *testing.T) {
	c := srContract(24050, "CE", 0.52, 0.001, -3, 0, testExpiry)
	g := &fakeGreeks{chain: []orderexec.OptionContract{c}}
	svc := deltaSvc(g, 2400000, "CE24050")
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax, svc.cfg.SRThetaMaxGainPct = 0.45, 0.60, 25
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)

	sig := buyCall(24000)
	sig.StrategyName = "NIFTY50_SR"
	err := svc.resolveEntryStrike(context.Background(), &sig, now)
	if err == nil || !strings.Contains(err.Error(), "refresh started") {
		t.Fatalf("cold chain: err=%v, want refusal with refresh", err)
	}
	waitUntil(t, "background refresh", func() bool { return g.n() == 1 && !svc.chainRefreshing.Load() })

	// The refresh stamps the cache with wall-clock time; move the stamp to
	// the test's clock.
	svc.chainMu.Lock()
	svc.chainAt = now
	svc.chainMu.Unlock()
	sig = buyCall(24000)
	sig.StrategyName = "NIFTY50_SR"
	if err := svc.resolveEntryStrike(context.Background(), &sig, now); err != nil {
		t.Fatal(err)
	}
	if sig.FNOToken != "CE24050" || g.n() != 1 {
		t.Fatalf("bought %s, chain loads %d", sig.FNOToken, g.n())
	}
}

// A stale cache is refused even though a chain source exists.
func TestEntryChainMaxAge(t *testing.T) {
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(24000, "CE", 0.5)}}
	svc := deltaSvc(g, 2400000)
	svc.cfg.EntryChainMaxAge = time.Minute
	t0 := time.Now()
	warmChain(t, svc, t0)
	if _, err := svc.entryChain(t0.Add(59 * time.Second)); err != nil {
		t.Fatalf("fresh chain refused: %v", err)
	}
	if _, err := svc.entryChain(t0.Add(61 * time.Second)); err == nil {
		t.Fatal("stale chain accepted")
	}
	waitUntil(t, "background refresh", func() bool { return !svc.chainRefreshing.Load() })
}

// offlineFake is an offlineResolver: Lookup* read what the "broker" side
// (ResolveStrike*) has resolved so far.
type offlineFake struct {
	mu     sync.Mutex
	known  map[int64]bool
	broker int
}

func (f *offlineFake) info(strike int64, opt string) orderexec.StrikeInfo {
	return orderexec.StrikeInfo{Token: opt + itoa(strike), Symbol: "NIFTY06OCT26" + itoa(strike) + opt, Strike: strike}
}

func (f *offlineFake) lookup(strike int64, opt string) (orderexec.StrikeInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info(strike, opt), f.known[strike]
}

func (f *offlineFake) LookupOn(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, bool) {
	return f.lookup(strike, opt)
}

func (f *offlineFake) LookupNearest(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, bool) {
	return f.lookup(strike, opt)
}

func (f *offlineFake) ResolveStrike(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broker++
	if f.known == nil {
		f.known = map[int64]bool{}
	}
	f.known[strike] = true
	return f.info(strike, opt), nil
}

func (f *offlineFake) brokerCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.broker
}

// A contract missing offline refuses the entry with no broker call on the
// signal path; one background lookup caches it for the next signal.
func TestResolveEntryContractNoBrokerOnSignalPath(t *testing.T) {
	r := &offlineFake{}
	svc := &Service{legResolver: r}
	now := time.Now()

	_, err := svc.resolveEntryContract(now, time.Time{}, 24000, "CE")
	if err == nil {
		t.Fatal("offline miss must refuse")
	}
	waitUntil(t, "background lookup", func() bool { return r.brokerCalls() == 1 && !svc.contractWarming.Load() })

	info, err := svc.resolveEntryContract(now, time.Time{}, 24000, "CE")
	if err != nil || info.Token != "CE24000" {
		t.Fatalf("after warm-up: %+v %v", info, err)
	}
	if r.brokerCalls() != 1 {
		t.Fatalf("broker calls = %d, want 1 (background only)", r.brokerCalls())
	}
}

// SENSEX: a stale chain with a working source refuses the entry instead of
// falling back to the ATM strike without greek checks.
func TestResolveExtraEntry_StaleChainRefuses(t *testing.T) {
	svc, ex := sensexService(t)
	res := &fakeExtraRes{}
	ex.res = res
	ex.greeks = &fakeGreeks{chain: []orderexec.OptionContract{
		srContract(81200, "CE", 0.52, 0.001, -20, 400, testExpiry),
	}}
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	sig := strategy.Signal{StrategyName: "SENSEX_SR", Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 81100}
	err := svc.resolveExtraEntry(context.Background(), ex, &sig, now)
	if !errors.Is(err, errChainStale) {
		t.Fatalf("err = %v, want errChainStale", err)
	}
	if sig.FNOToken != "" {
		t.Fatalf("stale chain bought %s", sig.FNOToken)
	}
	waitUntil(t, "background refresh", func() bool { return !ex.chainRefreshing.Load() })
}
