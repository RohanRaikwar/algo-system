package indengine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// startConsumer starts the Redis stream XREADGROUP consumer in a goroutine.
func (svc *Service) startConsumer(ctx context.Context) {
	if len(svc.streams) == 0 {
		return
	}
	go func() {
		if err := svc.redisReader.ConsumeTFCandles(ctx, svc.streams, svc.tfCandleCh); err != nil {
			log.Printf("[indengine] consumer error: %v", err)
		}
	}()
}

// startPELReclaimer starts periodic reclamation of stale PEL messages.
func (svc *Service) startPELReclaimer(ctx context.Context) {
	if len(svc.streams) == 0 {
		return
	}
	go svc.redisReader.StartPELReclaimer(ctx, svc.streams,
		svc.cfg.ConsumerGroup, svc.cfg.ConsumerName,
		time.Duration(svc.cfg.PELIntervalS)*time.Second,
		svc.cfg.PELMinIdleMs, svc.tfCandleCh,
		func(count int) {
			svc.prom.PELMessagesReclaimed.Add(float64(count))
			log.Printf("[indengine] reclaimed %d stale PEL messages", count)
		})
	log.Printf("[indengine] PEL reclaimer started (interval=%ds, minIdle=%dms)",
		svc.cfg.PELIntervalS, svc.cfg.PELMinIdleMs)
}

// processLoop owns the indicator engine: it consumes TF candles from the
// channel and runs commands (snapshot, reload) sent through onEngine, one
// at a time. Nothing else touches the engine while it runs.
func (svc *Service) processLoop(ctx context.Context) {
	defer close(svc.loopDone)
	const (
		indicatorLatencyKey           = "metrics:indengine:indicator_compute_ms"
		indicatorLatencyTTL           = 30 * time.Second
		indicatorLatencyPublishMinDur = 2 * time.Second
		indicatorLatencyAlpha         = 0.2
	)
	var (
		latencyEwmaMs      float64
		lastLatencyPublish time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-svc.cmds:
			fn()
		case tfc, ok := <-svc.tfCandleCh:
			if !ok {
				return
			}

			start := time.Now()
			results := svc.apply(ctx, tfc)
			elapsed := time.Since(start)
			svc.prom.IndicatorComputeDur.Observe(elapsed.Seconds())
			if len(results) > 0 {
				svc.prom.IndicatorsTotal.Add(float64(len(results)))
			}

			// Track EWMA latency and publish periodically
			latencyMs := float64(elapsed.Microseconds()) / 1000.0
			if latencyEwmaMs == 0 {
				latencyEwmaMs = latencyMs
			} else {
				latencyEwmaMs = latencyEwmaMs*(1.0-indicatorLatencyAlpha) + latencyMs*indicatorLatencyAlpha
			}
			if svc.redisWriter != nil && time.Since(lastLatencyPublish) >= indicatorLatencyPublishMinDur {
				cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				if cctx.Err() == nil {
					_ = svc.redisWriter.Client().Set(
						cctx,
						indicatorLatencyKey,
						fmt.Sprintf("%.3f", latencyEwmaMs),
						indicatorLatencyTTL,
					).Err()
				}
				cancel()
				lastLatencyPublish = time.Now()
			}
		}
	}
}

// errEngineStopped is returned by onEngine once processLoop has exited.
var errEngineStopped = errors.New("indicator engine stopped")

// onEngine runs fn on the engine's owner goroutine and waits for it to
// finish. It is the only way code outside processLoop may use the engine.
func (svc *Service) onEngine(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	select {
	case svc.cmds <- func() { defer close(done); fn() }:
	case <-ctx.Done():
		return ctx.Err()
	case <-svc.loopDone:
		return errEngineStopped
	}
	// Accepted: the owner runs fn to completion before anything else.
	<-done
	return nil
}
