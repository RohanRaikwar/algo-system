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
	if st, ok := book["status"].(bool); ok && !st {
		// status=false is an unreadable book, not an empty one.
		return nil, fmt.Errorf("broker positions status=false: %v", book["message"])
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

// ReconcileResult is what ReconcileAndFix did and what it left for a human.
type ReconcileResult struct {
	Fixed   []string           // drift corrected (position closed or reduced outside the executor)
	Waiting []PositionMismatch // safe drift seen once; fixed if the next pass reads the same
	Unsafe  []PositionMismatch // drift the executor must not act on
}

// ReconcileAndFix compares positions like ReconcilePositions and corrects
// the safe kind of drift: the broker holds LESS of a tracked position than
// the executor thinks (closed or reduced outside it — by hand, by a stop
// fill the executor missed). That only ever books a sale that already
// happened and resizes protection; it never places a trade. A fix needs
// the same broker reading on two consecutive passes, so one bad read can
// never drop a live position. Everything else (broker holds more, a
// position the executor does not know, a short) is Unsafe.
func (oe *OrderExecutor) ReconcileAndFix() (ReconcileResult, error) {
	var res ReconcileResult
	mm, err := oe.ReconcilePositions()
	if err != nil {
		return res, err
	}
	seen := make(map[string]int64, len(mm))
	oe.mu.Lock()
	prev := oe.driftSeen
	oe.mu.Unlock()
	for _, m := range mm {
		seen[m.Token] = m.Broker
		if m.Expected <= 0 || m.Broker < 0 || m.Broker >= m.Expected {
			res.Unsafe = append(res.Unsafe, m)
			continue
		}
		if p, ok := prev[m.Token]; !ok || p != m.Broker {
			res.Waiting = append(res.Waiting, m)
			continue
		}
		if msg, ok := oe.shrinkTo(m.Token, m.Broker); ok {
			res.Fixed = append(res.Fixed, msg)
			delete(seen, m.Token)
		} else {
			res.Unsafe = append(res.Unsafe, m)
		}
	}
	oe.mu.Lock()
	oe.driftSeen = seen
	oe.mu.Unlock()
	if len(res.Fixed) > 0 {
		oe.notifyState()
	}
	return res, nil
}

// shrinkTo brings the one tracked position on token down to held units:
// books the units sold outside the executor at LTP, resizes or closes it.
func (oe *OrderExecutor) shrinkTo(token string, held int64) (string, bool) {
	oe.mu.RLock()
	var keys []string
	for k, r := range oe.entryOrders {
		if r.Real && r.Token == token {
			keys = append(keys, k)
		}
	}
	oe.mu.RUnlock()
	if len(keys) != 1 {
		return "", false // several positions on one contract: needs a human
	}
	posKey := keys[0]
	kl := oe.keyLock(posKey)
	kl.Lock()
	defer kl.Unlock()
	rec, ok := oe.entry(posKey)
	if !ok || rec.Pending || rec.ExitOrderID != "" {
		return "", false // settling or exiting: its own path decides
	}
	sig := signalFor(rec)
	if oe.stopMode() && rec.SLOrderID != "" {
		// The stop may be what sold: cancel it and book its fills first.
		switch oe.cancelStopForExit(sig, posKey) {
		case stopFilled:
			return fmt.Sprintf("%s %s: closed by its stop", posKey, rec.Symbol), true
		case stopUnknown:
			return "", false
		}
		if rec, ok = oe.entry(posKey); !ok {
			return fmt.Sprintf("%s: closed by its stop", posKey), true
		}
	}
	if sold := rec.Quantity - held; sold > 0 {
		oe.reportFill(sig, "SELL", oe.GetLTP(rec.Token), sold, true)
	}
	if held <= 0 {
		oe.finalizeExit(sig)
		return fmt.Sprintf("%s %s: broker flat — closed (sold outside the executor, booked at LTP)", posKey, rec.Symbol), true
	}
	oe.updateEntry(posKey, func(r *OrderRecord) { r.Quantity = held })
	oe.rearmStop(posKey)
	return fmt.Sprintf("%s %s: broker holds %d of %d — reduced (booked at LTP)", posKey, rec.Symbol, held, rec.Quantity), true
}
