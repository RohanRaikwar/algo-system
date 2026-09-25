package orderexec

import (
	"errors"
	"testing"
	"time"
)

func scriptedBook(books ...map[string]any) (func() (map[string]any, error), *int) {
	calls := 0
	return func() (map[string]any, error) {
		i := calls
		calls++
		if i >= len(books) {
			i = len(books) - 1
		}
		if books[i] == nil {
			return nil, errors.New("book unavailable")
		}
		return books[i], nil
	}, &calls
}

func row(status string, avg any) map[string]any {
	return bookWith(map[string]any{"orderid": "OID-1", "orderstatus": status, "averageprice": avg, "text": "reason"})
}

func TestConfirmFill_CompleteUsesAveragePrice(t *testing.T) {
	book, _ := scriptedBook(row("open", 0.0), row("complete", 123.45))
	f, err := confirmFill(book, "OID-1", make([]time.Duration, 3))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if f.Status != "complete" || f.AvgPricePaise != 12345 {
		t.Fatalf("got %+v, want complete @ 12345 paise", f)
	}
}

func TestConfirmFill_LateRejection(t *testing.T) {
	book, _ := scriptedBook(row("rejected", 0.0))
	f, err := confirmFill(book, "OID-1", make([]time.Duration, 3))
	if err != nil || !f.Rejected() {
		t.Fatalf("want rejected, got %+v err=%v", f, err)
	}
}

func TestConfirmFill_StillOpenAfterPolling(t *testing.T) {
	book, calls := scriptedBook(row("open", 0.0))
	f, err := confirmFill(book, "OID-1", make([]time.Duration, 3))
	if err != nil || f.Status != "open" || f.Rejected() {
		t.Fatalf("want non-terminal open, got %+v err=%v", f, err)
	}
	if *calls != 3 {
		t.Fatalf("want 3 polls, got %d", *calls)
	}
}

func TestConfirmFill_BookUnavailable(t *testing.T) {
	book, _ := scriptedBook(nil)
	if _, err := confirmFill(book, "OID-1", make([]time.Duration, 2)); err == nil {
		t.Fatal("want error when book never readable")
	}
}

func TestRupeesToPaise(t *testing.T) {
	cases := map[any]int64{
		123.45: 12345, "123.45": 12345, 0.05: 5, "7": 700, 7.0: 700, "99.9": 9990, 1234.5: 123450,
	}
	for in, want := range cases {
		got, err := rupeesToPaise(in)
		if err != nil || got != want {
			t.Errorf("rupeesToPaise(%v) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := rupeesToPaise("abc"); err == nil {
		t.Error("want error for non-numeric")
	}
}
