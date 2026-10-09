package stratengine

import (
	"context"
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

type fakeFutures struct {
	inst *orderexec.Instrument
	err  error
}

func (f fakeFutures) NearestFuture(string, time.Time) (*orderexec.Instrument, error) { return f.inst, f.err }

func TestRefreshVolumeToken(t *testing.T) {
	var sent []string
	svc := &Service{cfg: Config{SREnabled: true, FNOExchange: "NFO"}, srStrategy: strategy.NewNifty50SR(1, strategy.DefaultNifty50SRConfig())}
	svc.futures = fakeFutures{inst: &orderexec.Instrument{Token: "35001", Symbol: "NIFTY28OCT26FUT"}}
	svc.subscribeHook = func(t []string) { sent = append(sent, t...) }
	svc.refreshVolumeToken(context.Background(), time.Now())
	if got := svc.srStrategy.Config().VolumeToken; got != "NFO:35001" {
		t.Fatalf("volume token = %q", got)
	}
	if len(sent) != 1 || sent[0] != "35001" {
		t.Fatalf("subscribed %v", sent)
	}

	svc2 := &Service{cfg: Config{SREnabled: true}, srStrategy: strategy.NewNifty50SR(1, strategy.DefaultNifty50SRConfig())}
	svc2.futures = fakeFutures{err: errors.New("not loaded")}
	svc2.refreshVolumeToken(context.Background(), time.Now())
	if svc2.srStrategy.Config().VolumeToken != "" {
		t.Fatal("no future must leave volume unset")
	}
}
