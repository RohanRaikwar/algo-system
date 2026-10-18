package stratengine

import (
	"log"
	"sync"
	"sync/atomic"

	"trading-systemv1/internal/strategy"
)

// sweepRow is one liquidity sweep waiting to be journaled.
type sweepRow struct {
	strategy string
	key      string
	sw       strategy.SRSweep
}

// sweepJournal is the part of the signal journal sweepWriter needs.
type sweepJournal interface {
	RecordSweep(strategy, key string, sw strategy.SRSweep) error
}

// sweepWriter journals SR liquidity sweeps off the candle path: the
// strategy sink only queues, one goroutine writes SQLite.
type sweepWriter struct {
	j       sweepJournal
	ch      chan sweepRow
	dropped atomic.Int64
	wg      sync.WaitGroup
}

func newSweepWriter(j sweepJournal) *sweepWriter {
	w := &sweepWriter{j: j, ch: make(chan sweepRow, 256)}
	w.wg.Add(1)
	go w.run()
	return w
}

func (w *sweepWriter) run() {
	defer w.wg.Done()
	for r := range w.ch {
		if err := w.j.RecordSweep(r.strategy, r.key, r.sw); err != nil {
			log.Printf("[stratengine] sweep journal %s %s: %v", r.strategy, r.key, err)
		}
	}
}

// sink returns the SetSweepSink callback for strategy name. It never
// blocks: a full queue drops the row.
func (w *sweepWriter) sink(name string) func(string, strategy.SRSweep) {
	return func(key string, sw strategy.SRSweep) {
		select {
		case w.ch <- sweepRow{strategy: name, key: key, sw: sw}:
		default:
			n := w.dropped.Add(1)
			log.Printf("[stratengine] sweep journal queue full, dropped %s %s %s (%d dropped)", name, key, sw.Side, n)
		}
	}
}

// close writes what is queued and stops the writer. Sinks must be
// cleared first.
func (w *sweepWriter) close() {
	close(w.ch)
	w.wg.Wait()
}

// wireSweepJournal journals every new sweep of each SR instance. Nothing
// is read back: a restart starts the day's sweep view empty, as before.
func (svc *Service) wireSweepJournal() {
	if svc.journal == nil {
		return
	}
	svc.sweeps = newSweepWriter(svc.journal)
	for _, sr := range svc.srAll() {
		sr.SetSweepSink(svc.sweeps.sink(sr.Name()))
	}
}
