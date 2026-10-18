package stratengine

import (
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

type blockingSweepJournal struct {
	mu      sync.Mutex
	rows    int
	release chan struct{}
}

func (b *blockingSweepJournal) RecordSweep(string, string, strategy.SRSweep) error {
	<-b.release
	b.mu.Lock()
	b.rows++
	b.mu.Unlock()
	return nil
}

// A stuck journal never blocks the strategy sink; close writes what was
// queued.
func TestSweepWriterSinkNeverBlocks(t *testing.T) {
	j := &blockingSweepJournal{release: make(chan struct{})}
	w := newSweepWriter(j)
	sink := w.sink("NIFTY50_SR")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			sink("NSE:99926000", strategy.SRSweep{Level: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sink blocked on a stuck journal")
	}
	if w.dropped.Load() == 0 {
		t.Error("nothing dropped past the queue size")
	}
	close(j.release)
	w.close()
	if want := 300 - int(w.dropped.Load()); j.rows != want {
		t.Errorf("wrote %d rows, want %d", j.rows, want)
	}
}
