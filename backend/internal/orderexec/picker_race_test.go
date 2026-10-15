package orderexec

import (
	"sync"
	"testing"

	"trading-systemv1/internal/model"
)

// TestSetStrikePickerConcurrentWithResolve: stratengine attaches a newly
// resolved picker from its ATM goroutine while order workers resolve
// instruments. Run with -race.
func TestSetStrikePickerConcurrentWithResolve(t *testing.T) {
	oe := liveExecutor(t, &fakeAPI{})
	sig := realSig(model.ActionBuy)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			oe.SetStrikePicker(NewStrikePicker(nil))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if _, _, err := oe.resolveInstrument(sig, "BUY"); err != nil {
				t.Errorf("resolveInstrument: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}
