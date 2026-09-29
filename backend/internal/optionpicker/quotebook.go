package optionpicker

import (
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// QuoteBook holds the latest quote per option token. Update is on the tick
// path: one map write, no allocation once a token is known.
type QuoteBook struct {
	maxAge time.Duration
	mu     sync.RWMutex
	q      map[string]Quote
}

func NewQuoteBook(maxAge time.Duration) *QuoteBook {
	return &QuoteBook{maxAge: maxAge, q: make(map[string]Quote, 128)}
}

// Update stores the tick's LTP and, when present, its bid/ask/OI. A packet
// without depth keeps the previous bid/ask (and its time).
func (b *QuoteBook) Update(t model.Tick) {
	b.mu.Lock()
	q := b.q[t.Token]
	if t.Price > 0 {
		q.LTP = t.Price
	}
	if !t.QuoteTS.IsZero() {
		q.Bid, q.Ask, q.At = t.BestBid, t.BestAsk, t.QuoteTS
	}
	if t.OI > 0 {
		q.OI = t.OI
	}
	b.q[t.Token] = q
	b.mu.Unlock()
}

func (b *QuoteBook) Get(token string) (Quote, bool) {
	b.mu.RLock()
	q, ok := b.q[token]
	b.mu.RUnlock()
	return q, ok
}

// BidAsk returns a fresh two-sided quote (for paper fills). A crossed quote
// (ask < bid) is a bad depth snapshot, not a tradable spread, so it is
// rejected too.
func (b *QuoteBook) BidAsk(token string, now time.Time) (bid, ask int64, ok bool) {
	q, found := b.Get(token)
	if !found || q.Bid <= 0 || q.Ask <= 0 || q.Ask < q.Bid || now.Sub(q.At) > b.maxAge {
		return 0, 0, false
	}
	return q.Bid, q.Ask, true
}
