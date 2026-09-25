package orderexec

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"trading-systemv1/pkg/smartconnect"
)

// ErrOrderStateUnknown means an order may or may not be live at the broker
// and the order book could not settle it. The order must NOT be resent and
// the position must NOT be assumed flat — an operator has to check the book.
var ErrOrderStateUnknown = errors.New("order state unknown")

// defaultLookupDelays spaces order-book reads after an ambiguous placement,
// giving the broker's book time to show an order that was accepted late.
var defaultLookupDelays = []time.Duration{0, time.Second, 2 * time.Second}

// retryTag is the ordertag of the single resend. A distinct tag keeps the
// first attempt's order-book row (e.g. rejected) from masking the retry's.
func retryTag(tag string) string { return tag + "R" }

// orderAttempt places one order and, if the broker's answer is ambiguous,
// settles its state from the order book by ordertag before any resend.
//
// A resend happens at most once, carries retryTag(tag), and only when the
// first attempt was a definitive broker rejection (e.g. expired token) or
// the book positively shows no live order carrying the first tag — checked
// again immediately before resending.
type orderAttempt struct {
	tag          string
	place        func(tag string) (string, error)
	orderBook    func() (map[string]any, error)
	refresh      func() error
	isFinal      func(error) bool // rejections that must not be retried
	lookupDelays []time.Duration  // nil = single immediate lookup
}

func (a orderAttempt) run() (string, error) {
	id, err := a.place(a.tag)
	ambiguous := errors.Is(err, smartconnect.ErrOutcomeUnknown)
	id, err = a.settle(a.tag, id, err)
	if err == nil || a.isFinal(err) || errors.Is(err, ErrOrderStateUnknown) {
		return id, err
	}
	// Resend only when it is provably safe and useful: the book showed the
	// first order absent, or the broker refused it for an expired session
	// (a refresh fixes that). Any other rejection (margin, bad params, …)
	// would just be refused again — or worse, taken twice.
	if !errors.Is(err, errNotInBook) && !isSessionError(err) {
		return "", err
	}

	if rerr := a.refresh(); rerr != nil {
		return "", fmt.Errorf("order+refresh failed: %w", err)
	}

	if ambiguous {
		// Last look right before resending: a slow book may only now show
		// the first order. If the book can't be read, don't risk a copy.
		book, berr := a.orderBook()
		if berr != nil {
			return "", fmt.Errorf("%w: tag=%s order book unreadable before resend: %v", ErrOrderStateUnknown, a.tag, berr)
		}
		if row, found := findOrderByTag(book, a.tag); found && !rowRejected(row) {
			oid, _ := row["orderid"].(string)
			return oid, nil
		}
	}

	rt := retryTag(a.tag)
	id, err = a.place(rt)
	id, err = a.settle(rt, id, err)
	if errors.Is(err, errNotInBook) {
		// Second ambiguous failure with nothing in the book: stop here rather
		// than risk a third copy of the order.
		return "", fmt.Errorf("%w: tags=%s,%s not in order book after retry", ErrOrderStateUnknown, a.tag, rt)
	}
	return id, err
}

// errNotInBook signals an ambiguous placement that the order book proved
// never reached the broker, so a resend is safe.
var errNotInBook = errors.New("order not in book")

// settle turns an ambiguous placement result into a definite one using the
// order book. Definite results pass through unchanged.
func (a orderAttempt) settle(tag, id string, err error) (string, error) {
	if err == nil || !errors.Is(err, smartconnect.ErrOutcomeUnknown) {
		return id, err
	}

	delays := a.lookupDelays
	if len(delays) == 0 {
		delays = []time.Duration{0}
	}
	var lookupErr error
	for _, d := range delays {
		if d > 0 {
			time.Sleep(d)
		}
		book, berr := a.orderBook()
		if berr != nil {
			lookupErr = berr
			continue
		}
		row, found := findOrderByTag(book, tag)
		if !found {
			continue
		}
		oid, _ := row["orderid"].(string)
		if rowRejected(row) {
			return "", fmt.Errorf("order %s %v by broker: %v", oid, row["orderstatus"], row["text"])
		}
		return oid, nil
	}
	if lookupErr != nil {
		return "", fmt.Errorf("%w: tag=%s placement %v, order book %v", ErrOrderStateUnknown, tag, err, lookupErr)
	}
	return "", fmt.Errorf("%w: tag=%s: %v", errNotInBook, tag, err)
}

// isSessionError reports a broker rejection caused by an expired or
// invalid session token — the one definite rejection a refresh can cure.
func isSessionError(err error) bool {
	s := strings.ToLower(err.Error())
	for _, m := range []string{"tokenexception", "invalid token", "ag8001", "ag8002", "ag8003", "session expired"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func rowRejected(row map[string]any) bool {
	switch strings.ToLower(fmt.Sprint(row["orderstatus"])) {
	case "rejected", "cancelled":
		return true
	}
	return false
}

// findOrderByTag scans an Angel One getOrderBook response for the row whose
// ordertag matches. "data" is null when the book is empty.
func findOrderByTag(book map[string]any, tag string) (map[string]any, bool) {
	rows, _ := book["data"].([]any)
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if ok && row["ordertag"] == tag {
			return row, true
		}
	}
	return nil, false
}
