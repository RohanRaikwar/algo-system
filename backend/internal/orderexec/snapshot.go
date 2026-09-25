package orderexec

import (
	"encoding/json"
	"fmt"

	"trading-systemv1/internal/strategy"
)

// executorSnapshot is the executor state that must survive a restart: which
// side is open, the exact contract each position was entered in, and the
// entry order (with its paired GTT rule IDs). Without it an exit after a
// restart falls back to the current strike mapping — possibly a different
// contract — and the paired GTT rules are never cancelled.
type executorSnapshot struct {
	InCallPosition bool                     `json:"in_call_position"`
	InPutPosition  bool                     `json:"in_put_position"`
	PositionInst   map[string]fnoInstrument `json:"position_inst"`
	EntryOrders    map[string]OrderRecord   `json:"entry_orders"`
}

// Snapshot serialises executor position state for persistence.
func (oe *OrderExecutor) Snapshot() ([]byte, error) {
	inCall, inPut := oe.sideOpen(strategy.SideCall), oe.sideOpen(strategy.SidePut)
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	return json.Marshal(executorSnapshot{
		InCallPosition: inCall, // informational; side state is derived from entries
		InPutPosition:  inPut,
		PositionInst:   oe.positionInst,
		EntryOrders:    oe.entryOrders,
	})
}

// Restore replaces executor position state from a Snapshot. On error the
// current state is left untouched.
func (oe *OrderExecutor) Restore(data []byte) error {
	var snap executorSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("order executor restore: %w", err)
	}
	if snap.PositionInst == nil {
		snap.PositionInst = make(map[string]fnoInstrument)
	}
	if snap.EntryOrders == nil {
		snap.EntryOrders = make(map[string]OrderRecord)
	}

	oe.mu.Lock()
	defer oe.mu.Unlock()
	oe.positionInst = snap.PositionInst
	oe.entryOrders = snap.EntryOrders
	return nil
}
