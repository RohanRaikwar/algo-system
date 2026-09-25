package orderexec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// defaultFillPollDelays spaces order-book reads after a placement is
// accepted. A MARKET order normally reaches a terminal status well inside
// this window (~3s total).
var defaultFillPollDelays = []time.Duration{
	200 * time.Millisecond, 300 * time.Millisecond, 500 * time.Millisecond, time.Second, time.Second,
}

// fillResult is what the broker's order book says happened to an order
// after it was accepted.
type fillResult struct {
	Status        string // lower-cased orderstatus: complete, rejected, cancelled, open, ...
	AvgPricePaise int64  // average fill price; 0 when not filled
	FilledQty     int64  // filledshares; 0 when the broker didn't report it
	Text          string // broker's reason text (rejections)
}

// Rejected reports whether the order ended with nothing filled. A cancel
// after a partial fill is not a rejection: the filled part is a position.
func (f fillResult) Rejected() bool {
	return f.Status == "rejected" || (f.Status == "cancelled" && f.FilledQty == 0)
}

// Partial reports an order that ended (cancelled) after filling only part.
func (f fillResult) Partial() bool {
	return f.Status == "cancelled" && f.FilledQty > 0
}

func (f fillResult) terminal() bool {
	return f.Status == "complete" || f.Status == "rejected" || f.Status == "cancelled"
}

// parseFillRow reads the fill fields of one order-book row.
func parseFillRow(row map[string]any) fillResult {
	f := fillResult{
		Status: strings.ToLower(fmt.Sprint(row["orderstatus"])),
		Text:   toStr(row["text"]),
	}
	if q, err := strconv.ParseInt(strings.TrimSpace(toStr(row["filledshares"])), 10, 64); err == nil {
		f.FilledQty = q
	}
	if f.Status == "complete" || f.FilledQty > 0 {
		if p, err := rupeesToPaise(row["averageprice"]); err == nil {
			f.AvgPricePaise = p
		}
	}
	return f
}

// confirmFill polls the order book until orderID reaches a terminal status
// or the delays run out. An accepted placement is not a fill: Angel One
// returns an orderid and can reject the order moments later (e.g. margin).
// If polling ends on a non-terminal status, that status is returned without
// error. An error means the book was never readable.
func confirmFill(orderBook func() (map[string]any, error), orderID string, delays []time.Duration) (fillResult, error) {
	var last fillResult
	var lastErr error
	seen := false
	for _, d := range delays {
		if d > 0 {
			time.Sleep(d)
		}
		book, err := orderBook()
		if err != nil {
			lastErr = err
			continue
		}
		row, ok := findOrderByID(book, orderID)
		if !ok {
			continue
		}
		seen = true
		last = parseFillRow(row)
		if last.terminal() {
			return last, nil
		}
	}
	if !seen {
		if lastErr == nil {
			lastErr = errors.New("order not found in order book")
		}
		return fillResult{}, fmt.Errorf("confirm fill %s: %w", orderID, lastErr)
	}
	return last, nil
}

func findOrderByID(book map[string]any, orderID string) (map[string]any, bool) {
	rows, _ := book["data"].([]any)
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if ok && row["orderid"] == orderID {
			return row, true
		}
	}
	return nil, false
}

// rupeesToPaise converts a broker rupee amount (JSON number or string) to
// paise by parsing its decimal text, so no float arithmetic touches money.
// A float64 formats as its shortest round-trip decimal, which is exactly the
// broker's two-decimal value.
func rupeesToPaise(v any) (int64, error) {
	var s string
	switch t := v.(type) {
	case float64:
		s = strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		s = strings.TrimSpace(t)
	default:
		return 0, fmt.Errorf("unsupported price type %T", v)
	}
	whole, frac, _ := strings.Cut(s, ".")
	roundUp := false
	if len(frac) > 2 {
		// Multi-trade averages can carry more decimals: round half up to the
		// paisa on the digit text, still with no float arithmetic.
		roundUp = frac[2] >= '5'
		frac = frac[:2]
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("price %q: %w", s, err)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("price %q: %w", s, err)
	}
	p := w*100 + f
	if roundUp {
		p++
	}
	return p, nil
}
