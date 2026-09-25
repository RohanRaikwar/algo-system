package orderexec

import (
	"fmt"
	"sync"
	"time"
)

// orderBookMinInterval spaces getOrderBook calls from all goroutines of one
// executor (inline settle, fill polls, background settlers). Angel One rate
// limits this endpoint; hammering it turns reads into 403s.
var orderBookMinInterval = time.Second

// bookReader keeps order-book reads at least min apart across all callers
// of one executor, and lets order-path (inline) reads go ahead of background
// pollers. No lock is held during the HTTP call. Every read fetches fresh
// data: a cached book fetched before the caller asked could hide an order
// the caller just placed and trigger a resend.
type bookReader struct {
	mu          sync.Mutex
	fetch       func() (map[string]any, error)
	min         time.Duration
	lastStart   time.Time
	highWaiting int
}

func newBookReader(fetch func() (map[string]any, error), min time.Duration) *bookReader {
	return &bookReader{fetch: fetch, min: min}
}

// get is an inline (order-path) read.
func (r *bookReader) get() (map[string]any, error) {
	r.acquire(true)
	return r.fetch()
}

// getBackground is a background poller's read; it yields to inline reads.
func (r *bookReader) getBackground() (map[string]any, error) {
	r.acquire(false)
	return r.fetch()
}

// acquire waits for this caller's turn: the spacing interval has passed
// and, for a background read, no inline read is waiting.
func (r *bookReader) acquire(high bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if high {
		r.highWaiting++
		defer func() { r.highWaiting-- }()
	}
	for {
		wait := time.Duration(0)
		if !r.lastStart.IsZero() {
			wait = time.Until(r.lastStart.Add(r.min))
		}
		if wait <= 0 && (high || r.highWaiting == 0) {
			r.lastStart = time.Now()
			return
		}
		if wait <= 0 || wait > 2*time.Millisecond {
			wait = 2 * time.Millisecond
		}
		r.mu.Unlock()
		time.Sleep(wait)
		r.mu.Lock()
	}
}

// readOrderBook is the executor's only way to read the order book.
func (oe *OrderExecutor) readOrderBook() (map[string]any, error) {
	return oe.bookReaderLazy().get()
}

// readOrderBookBackground is readOrderBook for background settlers; it
// yields to order-path reads.
func (oe *OrderExecutor) readOrderBookBackground() (map[string]any, error) {
	return oe.bookReaderLazy().getBackground()
}

func (oe *OrderExecutor) bookReaderLazy() *bookReader {
	oe.bookOnce.Do(func() {
		oe.books = newBookReader(func() (map[string]any, error) {
			api := oe.brokerAPI()
			if api == nil {
				return nil, errNoSession
			}
			book, err := api.OrderBook()
			if err != nil {
				return nil, err
			}
			// Angel answers some failures with HTTP 200 and status:false and
			// no data. That is an unreadable book, not an empty one — reading
			// it as empty would "prove" an order absent and trigger a resend.
			if st, _ := book["status"].(bool); !st {
				return nil, fmt.Errorf("order book status=false: %v (%v)", book["message"], book["errorcode"])
			}
			return book, nil
		}, orderBookMinInterval)
	})
	return oe.books
}
