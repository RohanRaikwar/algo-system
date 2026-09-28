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
	svc := &Service{cfg: Config{RangeEnabled: true, FNOExchange: "NFO"}, nifty50RangeStrategy: strategy.NewNifty50Range(1)}
	svc.futures = fakeFutures{inst: &orderexec.Instrument{Token: "35001", Symbol: "NIFTY28OCT26FUT"}}
	svc.subscribeHook = func(t []string) { sent = append(sent, t...) }
	svc.refreshVolumeToken(context.Background(), time.Now())
	if got := svc.nifty50RangeStrategy.Config().VolumeToken; got != "NFO:35001" {
		t.Fatalf("volume token = %q", got)
	}
	if len(sent) != 1 || sent[0] != "35001" {
		t.Fatalf("subscribed %v", sent)
	}

	svc2 := &Service{cfg: Config{RangeEnabled: true}, nifty50RangeStrategy: strategy.NewNifty50Range(1)}
	svc2.futures = fakeFutures{err: errors.New("not loaded")}
	svc2.refreshVolumeToken(context.Background(), time.Now())
	if svc2.nifty50RangeStrategy.Config().VolumeToken != "" {
		t.Fatal("no future must leave volume unset")
	}
}
