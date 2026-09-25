package stratengine

import (
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/strategy"
)

func TestServiceSyncStrategyFNOTokensQualifiesExchange(t *testing.T) {
	svc := &Service{
		cfg:               Config{FNOExchange: "NFO"},
		nifty50SLStrategy: strategy.NewNifty50FnOSL(1),
	}

	svc.syncStrategyFNOTokens("62582", "62587")

	if got := svc.nifty50SLStrategy.Config().FNOCallToken; got != "NFO:62582" {
		t.Fatalf("nifty50 sl call token = %q, want %q", got, "NFO:62582")
	}
	if got := svc.nifty50SLStrategy.Config().FNOPutToken; got != "NFO:62587" {
		t.Fatalf("nifty50 sl put token = %q, want %q", got, "NFO:62587")
	}
}

func TestServiceNormalizeExitInstrumentUsesTrackedEntry(t *testing.T) {
	svc := &Service{}
	sig := strategy.Signal{
		StrategyName: "NIFTY50_FNO_SL",
		Action:       strategy.ActionExit,
		Side:         strategy.SideCall,
		Token:        "62582",
		Exchange:     "NFO",
	}

	svc.normalizeExitInstrument(&sig, map[string]trackedInstrument{
		"NIFTY50_FNO_SL|CALL": {token: "99926000", exchange: "NSE"},
	}, "NIFTY50_FNO_SL|CALL")

	if sig.Token != "99926000" || sig.Exchange != "NSE" {
		t.Fatalf("normalized signal = %s:%s, want NSE:99926000", sig.Exchange, sig.Token)
	}
}

func TestComputeLiveStoplossCallTrailOverridesHard(t *testing.T) {
	stop, kind := computeLiveStoploss(strategy.SideCall, 50000, 52000, 2.0, 1.5, 1.0)
	if stop != 51220 {
		t.Fatalf("stop = %d, want %d", stop, 51220)
	}
	if kind != "TRAIL" {
		t.Fatalf("kind = %q, want %q", kind, "TRAIL")
	}
}

func TestComputeLiveStoplossPutFallsBackToHardWhenBestDrops(t *testing.T) {
	stop, kind := computeLiveStoploss(strategy.SidePut, 50000, 49000, 2.0, 1.5, 1.0)
	if stop != 49000 {
		t.Fatalf("stop = %d, want %d", stop, 49000)
	}
	if kind != "HARD" {
		t.Fatalf("kind = %q, want %q", kind, "HARD")
	}
}

func TestComputeLiveStoplossCallMatchesRequestedPremiumExample(t *testing.T) {
	stop, kind := computeLiveStoploss(strategy.SideCall, 20000, 20000, 10.0, 5.0, 0.0)
	if stop != 19000 {
		t.Fatalf("stop = %d, want %d", stop, 19000)
	}
	if kind != "TRAIL" {
		t.Fatalf("kind = %q, want %q", kind, "TRAIL")
	}
}

func TestComputeLiveStoplossCallHardFallbackMatchesRequestedPremiumExample(t *testing.T) {
	stop, kind := computeLiveStoploss(strategy.SideCall, 20000, 20000, 10.0, 0.0, 0.0)
	if stop != 18000 {
		t.Fatalf("stop = %d, want %d", stop, 18000)
	}
	if kind != "HARD" {
		t.Fatalf("kind = %q, want %q", kind, "HARD")
	}
}

func TestMarketHoursGuardsEntriesAfterClose(t *testing.T) {
	cases := []struct {
		name string
		ts   time.Time
		open bool
	}{
		{
			name: "pre_open",
			ts:   time.Date(2026, time.March, 20, 9, 14, 59, 0, markethours.IST),
			open: false,
		},
		{
			name: "during_session",
			ts:   time.Date(2026, time.March, 20, 15, 29, 59, 0, markethours.IST),
			open: true,
		},
		{
			name: "at_close",
			ts:   time.Date(2026, time.March, 20, 15, 30, 0, 0, markethours.IST),
			open: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := markethours.IsMarketOpen(tc.ts); got != tc.open {
				t.Fatalf("IsMarketOpen(%s) = %v, want %v", tc.ts.Format(time.RFC3339), got, tc.open)
			}
		})
	}
}

func TestExpandReverseSignals_ExitThenBuyOpposite(t *testing.T) {
	exitSig := strategy.Signal{
		StrategyName: "NIFTY50_FNO",
		Action:       strategy.ActionExit,
		Side:         strategy.SideCall,
		ReverseTo:    strategy.SidePut,
		Token:        "99926000",
		Exchange:     "NSE",
		Qty:          1,
		Reason:       "EXIT CALL reason=REVERSE_TO_PUT ema6=11900 sma21=12000 close=11950",
	}

	expanded := expandReverseSignals(exitSig)
	if len(expanded) != 2 {
		t.Fatalf("expanded signals len = %d, want 2", len(expanded))
	}
	if expanded[0].Action != strategy.ActionExit || expanded[0].Side != strategy.SideCall {
		t.Fatalf("expanded[0] = %+v, want EXIT CALL", expanded[0])
	}
	if expanded[1].Action != strategy.ActionBuy || expanded[1].Side != strategy.SidePut {
		t.Fatalf("expanded[1] = %+v, want BUY PUT", expanded[1])
	}
	if expanded[1].ReverseTo != strategy.SideNone {
		t.Fatalf("BUY reverse_to should be NONE, got %s", expanded[1].ReverseTo)
	}
}

func TestExpandReverseSignals_NoReverseKeepsSingleSignal(t *testing.T) {
	sig := strategy.Signal{Action: strategy.ActionExit, Side: strategy.SideCall}
	expanded := expandReverseSignals(sig)
	if len(expanded) != 1 {
		t.Fatalf("expanded signals len = %d, want 1", len(expanded))
	}
}
