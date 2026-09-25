package orderexec

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// expectedPosition is a real position the executor believes is open.
type expectedPosition struct {
	Symbol string
	Qty    int64
}

// PositionMismatch is a disagreement between the executor's view and the
// broker's net position for one instrument. Expected 0 means the broker
// holds a position the executor does not know about.
type PositionMismatch struct {
	Token    string
	Symbol   string
	Expected int64
	Broker   int64
}

func (m PositionMismatch) String() string {
	return fmt.Sprintf("%s (%s): executor=%d broker=%d", m.Symbol, m.Token, m.Expected, m.Broker)
}

// ErrNotLive is returned by ReconcilePositions when no broker session exists.
var ErrNotLive = errors.New("order executor not live")

// ReconcilePositions compares the executor's real open positions with the
// broker's net positions. It only reports; it never places or cancels
// orders, because the right action on a mismatch needs a human.
func (oe *OrderExecutor) ReconcilePositions() ([]PositionMismatch, error) {
	oe.mu.RLock()
	live, api := oe.live && oe.sessionOK, oe.api
	expected := make(map[string]expectedPosition)
	settling := make(map[string]bool)
	for _, rec := range oe.entryOrders {
		if rec.Real && rec.Pending {
			settling[rec.Token] = true
		}
		if !rec.Real || rec.Pending {
			continue // pending BUYs aren't known to be held yet
		}
		e := expected[rec.Token]
		e.Symbol = rec.Symbol
		e.Qty += rec.Quantity
		expected[rec.Token] = e
	}
	oe.mu.RUnlock()

	if !live || api == nil {
		return nil, ErrNotLive
	}
	book, err := api.Position()
	if err != nil {
		return nil, fmt.Errorf("broker positions: %w", err)
	}
	mm, err := comparePositions(expected, book)
	if err != nil {
		return nil, err
	}
	// A token whose BUY is still settling is neither expected nor
	// unexpected yet; its settlement reports the outcome.
	out := mm[:0]
	for _, m := range mm {
		if !settling[m.Token] {
			out = append(out, m)
		}
	}
	return out, nil
}

// comparePositions diffs expected positions against an Angel One
// getPosition response. A token missing from the response counts as flat.
func comparePositions(expected map[string]expectedPosition, book map[string]any) ([]PositionMismatch, error) {
	broker := make(map[string]expectedPosition)
	rows, _ := book["data"].([]any)
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		token := fmt.Sprint(row["symboltoken"])
		qty, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(row["netqty"])), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("position %s netqty %v: %w", token, row["netqty"], err)
		}
		b := broker[token]
		b.Symbol = fmt.Sprint(row["tradingsymbol"])
		b.Qty += qty
		broker[token] = b
	}

	var out []PositionMismatch
	for token, e := range expected {
		if b := broker[token]; b.Qty != e.Qty {
			out = append(out, PositionMismatch{Token: token, Symbol: e.Symbol, Expected: e.Qty, Broker: b.Qty})
		}
	}
	for token, b := range broker {
		if _, known := expected[token]; !known && b.Qty != 0 {
			out = append(out, PositionMismatch{Token: token, Symbol: b.Symbol, Broker: b.Qty})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out, nil
}

// brokerNetQty returns the broker's net quantity for one instrument token
// (0 when the broker lists no position for it).
func (oe *OrderExecutor) brokerNetQty(token string) (int64, error) {
	api := oe.brokerAPI()
	if api == nil {
		return 0, ErrNotLive
	}
	book, err := api.Position()
	if err != nil {
		return 0, fmt.Errorf("broker positions: %w", err)
	}
	if st, ok := book["status"].(bool); ok && !st {
		return 0, fmt.Errorf("broker positions status=false: %v", book["message"])
	}
	mm, err := comparePositions(map[string]expectedPosition{token: {}}, book)
	if err != nil {
		return 0, err
	}
	for _, m := range mm {
		if m.Token == token {
			return m.Broker, nil
		}
	}
	return 0, nil
}
