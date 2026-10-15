package orderexec

import (
	"sync"
	"testing"

	"trading-systemv1/internal/model"
)

// fakeContracts is a resolved ContractSource.
type fakeContracts struct{}

func (fakeContracts) Resolved() bool                 { return true }
func (fakeContracts) CallContract() (string, string) { return "333", "NIFTY_ATM_CE" }
func (fakeContracts) PutContract() (string, string)  { return "444", "NIFTY_ATM_PE" }

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
			oe.SetStrikePicker(fakeContracts{})
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

// Without Config.LotSizeOf a real entry's size is unknown: refuse.
func TestNoLotSizerRefusesRealEntry(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, LiveLots: 1, LogPrefix: "[test]"})
	if _, err := oe.realEntryQty("NIFTY13OCT2624000CE"); err == nil {
		t.Fatal("real entry sized without a lot sizer")
	}
}
