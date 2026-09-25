package orderexec

import (
	"errors"
	"fmt"
	"testing"

	"trading-systemv1/pkg/smartconnect"
)

// fakeBroker scripts PlaceOrder results and serves a fixed order book.
type fakeBroker struct {
	placeResults []error // error per call; nil = success
	placeCalls   int
	tags         []string
	book         map[string]any
	bookErr      error
	bookCalls    int
	refreshCalls int
}

func (f *fakeBroker) attempt(tag string) orderAttempt {
	return orderAttempt{
		tag: tag,
		place: func(tag string) (string, error) {
			f.tags = append(f.tags, tag)
			i := f.placeCalls
			f.placeCalls++
			if i < len(f.placeResults) && f.placeResults[i] != nil {
				return "", f.placeResults[i]
			}
			return fmt.Sprintf("OID-%d", i+1), nil
		},
		orderBook: func() (map[string]any, error) {
			f.bookCalls++
			return f.book, f.bookErr
		},
		refresh: func() error {
			f.refreshCalls++
			return nil
		},
		isFinal: isPositionFlatError,
	}
}

func bookWith(rows ...map[string]any) map[string]any {
	data := make([]any, len(rows))
	for i, r := range rows {
		data[i] = r
	}
	return map[string]any{"status": true, "data": data}
}

var errTimeout = fmt.Errorf("%w: context deadline exceeded", smartconnect.ErrOutcomeUnknown)

func TestOrderAttempt_SuccessFirstTry(t *testing.T) {
	f := &fakeBroker{}
	id, err := f.attempt("tag1").run()
	if err != nil || id != "OID-1" {
		t.Fatalf("got %q, %v", id, err)
	}
	if f.placeCalls != 1 || f.bookCalls != 0 {
		t.Fatalf("place=%d book=%d, want 1/0", f.placeCalls, f.bookCalls)
	}
}

func TestOrderAttempt_TimeoutButOrderInBook_NoDuplicate(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errTimeout},
		book: bookWith(
			map[string]any{"orderid": "OTHER", "ordertag": "zzz", "orderstatus": "complete"},
			map[string]any{"orderid": "BROKER-42", "ordertag": "tag1", "orderstatus": "complete"},
		),
	}
	id, err := f.attempt("tag1").run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "BROKER-42" {
		t.Fatalf("want order id recovered from book, got %q", id)
	}
	if f.placeCalls != 1 {
		t.Fatalf("order placed %d times — duplicate after timeout", f.placeCalls)
	}
}

func TestOrderAttempt_TimeoutAndBookUnavailable_DoesNotRetry(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errTimeout},
		bookErr:      errors.New("order book down"),
	}
	_, err := f.attempt("tag1").run()
	if !errors.Is(err, ErrOrderStateUnknown) {
		t.Fatalf("want ErrOrderStateUnknown, got %v", err)
	}
	if f.placeCalls != 1 {
		t.Fatalf("must not re-place when state unknown, placed %d times", f.placeCalls)
	}
}

func TestOrderAttempt_TimeoutAndNotInBook_RetriesWithRetryTag(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errTimeout},
		book:         bookWith(map[string]any{"orderid": "OTHER", "ordertag": "zzz"}),
	}
	id, err := f.attempt("tag1").run()
	if err != nil || id != "OID-2" {
		t.Fatalf("got %q, %v", id, err)
	}
	if f.placeCalls != 2 {
		t.Fatalf("want 1 retry, placed %d times", f.placeCalls)
	}
	if f.tags[0] != "tag1" || f.tags[1] != retryTag("tag1") {
		t.Fatalf("retry must use the retry tag, got %v", f.tags)
	}
}

func TestOrderAttempt_EmptyBookDataNull_Retries(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errTimeout},
		book:         map[string]any{"status": true, "data": nil},
	}
	if _, err := f.attempt("tag1").run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.placeCalls != 2 {
		t.Fatalf("placed %d times, want 2", f.placeCalls)
	}
}

func TestOrderAttempt_TimeoutAndRejectedInBook_TreatedAsRejection(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errTimeout},
		book: bookWith(map[string]any{
			"orderid": "BROKER-9", "ordertag": "tag1", "orderstatus": "rejected",
			"text": "AB1018: No Holdings Available",
		}),
	}
	_, err := f.attempt("tag1").run()
	if err == nil || !isPositionFlatError(err) {
		t.Fatalf("want position-flat rejection surfaced, got %v", err)
	}
	if f.placeCalls != 1 {
		t.Fatalf("final rejection must not retry, placed %d times", f.placeCalls)
	}
}

func TestOrderAttempt_DefinitiveRejection_RefreshesAndRetries(t *testing.T) {
	f := &fakeBroker{placeResults: []error{errors.New("TokenException: Invalid Token")}}
	id, err := f.attempt("tag1").run()
	if err != nil || id != "OID-2" {
		t.Fatalf("got %q, %v", id, err)
	}
	if f.refreshCalls != 1 || f.bookCalls != 0 {
		t.Fatalf("refresh=%d book=%d, want 1/0", f.refreshCalls, f.bookCalls)
	}
}

func TestOrderAttempt_PositionFlat_NoRetry(t *testing.T) {
	f := &fakeBroker{placeResults: []error{errors.New("AB1018 No Holdings Available")}}
	_, err := f.attempt("tag1").run()
	if !isPositionFlatError(err) {
		t.Fatalf("want position-flat error, got %v", err)
	}
	if f.placeCalls != 1 || f.refreshCalls != 0 {
		t.Fatalf("place=%d refresh=%d, want 1/0", f.placeCalls, f.refreshCalls)
	}
}

func TestOrderAttempt_RetryAlsoTimesOutAndNotInBook_StateUnknown(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errTimeout, errTimeout},
		book:         bookWith(),
	}
	_, err := f.attempt("tag1").run()
	if !errors.Is(err, ErrOrderStateUnknown) {
		t.Fatalf("want ErrOrderStateUnknown, got %v", err)
	}
	if f.placeCalls != 2 {
		t.Fatalf("placed %d times, want 2 (no third attempt)", f.placeCalls)
	}
}

func TestBuildOrderParams_IncludesOrderTag(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", FNOOrderType: "MARKET", FNOProductType: "INTRADAY"})
	p := oe.buildOrderParams("NIFTYCE", "123", "BUY", 0, 75, "abcd1234abcd1234")
	if p["ordertag"] != "abcd1234abcd1234" {
		t.Fatalf("ordertag missing: %v", p)
	}
}

// #5 — attempt 1 rejected, attempt 2 ambiguous but filled: the rejected
// row for the first tag must not mask the live second order.
func TestOrderAttempt_RetryFilled_NotMaskedByFirstRejection(t *testing.T) {
	f := &fakeBroker{
		placeResults: []error{errors.New("TokenException: Invalid Token"), errTimeout},
		book: bookWith(
			map[string]any{"orderid": "B-1", "ordertag": "tag1", "orderstatus": "rejected", "text": "Invalid Token"},
			map[string]any{"orderid": "B-2", "ordertag": retryTag("tag1"), "orderstatus": "complete"},
		),
	}
	id, err := f.attempt("tag1").run()
	if err != nil || id != "B-2" {
		t.Fatalf("want live retry order B-2, got %q err=%v", id, err)
	}
}

// #6 — just before resending, re-read the book for the first tag: a slow
// book that shows the order late must not produce a duplicate.
func TestOrderAttempt_RechecksBookBeforeResend(t *testing.T) {
	f := &fakeBroker{placeResults: []error{errTimeout}}
	a := f.attempt("tag1")
	calls := 0
	a.orderBook = func() (map[string]any, error) {
		calls++
		if calls == 1 {
			return bookWith(), nil // book lags on first read
		}
		return bookWith(map[string]any{"orderid": "LATE-1", "ordertag": "tag1", "orderstatus": "complete"}), nil
	}
	id, err := a.run()
	if err != nil || id != "LATE-1" {
		t.Fatalf("want late-appearing LATE-1, got %q err=%v", id, err)
	}
	if f.placeCalls != 1 {
		t.Fatalf("resent despite order appearing in book: placed %d", f.placeCalls)
	}
}
