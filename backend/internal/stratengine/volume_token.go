package stratengine

import (
	"context"
	"log"
	"time"

	"trading-systemv1/internal/orderexec"
)

// futureResolver finds the current NIFTY future. Satisfied by
// *orderexec.InstrumentMaster.
type futureResolver interface {
	NearestFuture(name string, now time.Time) (*orderexec.Instrument, error)
}

// volumeTokenSetter is a strategy that takes volume from another instrument.
type volumeTokenSetter interface {
	SetVolumeToken(token string)
}

// refreshVolumeToken points NIFTY50_SR's volume (and SR's VWAP) at the nearest
// NIFTY future (the index has no volume) and subscribes its ticks. Without
// a future the volume checks stay inactive (RequireVolume=false).
func (svc *Service) refreshVolumeToken(ctx context.Context, now time.Time) {
	var setters []volumeTokenSetter
	if svc.cfg.SREnabled && svc.srStrategy != nil {
		setters = append(setters, svc.srStrategy)
	}
	if len(setters) == 0 {
		return
	}
	res := svc.futures
	if res == nil {
		res = orderexec.GetInstrumentMaster()
	}
	fut, err := res.NearestFuture("NIFTY", now)
	if err != nil {
		log.Printf("[stratengine] range volume: no NIFTY future (%v) — volume checks inactive", err)
		return
	}
	for _, s := range setters {
		s.SetVolumeToken(svc.qualifyFNOToken(fut.Token))
	}
	svc.subscribeTokens(ctx, fut.Token)
	log.Printf("[stratengine] range volume from %s (token=%s)", fut.Symbol, fut.Token)
}
