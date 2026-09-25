package orderexec

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

// fakeAPI is a scripted broker. Orders are recorded; the order book is
// whatever rows the test puts in it.
type fakeAPI struct {
	mu        sync.Mutex
	placed    []map[string]any
	placeErr  []error         // per call; nil = accepted
	placeGate chan struct{}   // if set, PlaceOrder blocks until closed
	rows      []map[string]any // order book rows
	bookErr   error
	gttNext   int
	gttMade   []string
	gttCancel []string
	positions []map[string]any

	// autoStatus, when set, adds an order-book row for each accepted order.
	autoStatus string
	autoAvg    float64
	autoFilled string // filledshares for auto rows; "" = omit

	bookStatusFalse bool // OrderBook replies {"status":false} (Angel AB1004 style)

	cancelErr   []error // per GTTCancelRule call; nil = success
	cancelCalls int
}

func (f *fakeAPI) PlaceOrder(p map[string]any) (string, error) {
	if f.placeGate != nil {
		<-f.placeGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := len(f.placed)
	f.placed = append(f.placed, p)
	if i < len(f.placeErr) && f.placeErr[i] != nil {
		return "", f.placeErr[i]
	}
	id := fmt.Sprintf("OID-%d", i+1)
	if f.autoStatus != "" {
		row := map[string]any{"orderid": id, "ordertag": p["ordertag"], "orderstatus": f.autoStatus, "averageprice": f.autoAvg}
		if f.autoFilled != "" {
			row["filledshares"] = f.autoFilled
		}
		f.rows = append(f.rows, row)
	}
	return id, nil
}

func (f *fakeAPI) PlaceOrderPaperTrade(p map[string]any) (string, error) { return f.PlaceOrder(p) }

func (f *fakeAPI) OrderBook() (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bookErr != nil {
		return nil, f.bookErr
	}
	if f.bookStatusFalse {
		return map[string]any{"status": false, "message": "Something Went Wrong", "errorcode": "AB1004", "data": nil}, nil
	}
	data := make([]any, len(f.rows))
	for i, r := range f.rows {
		cp := make(map[string]any, len(r))
		for k, v := range r {
			cp[k] = v
		}
		data[i] = cp
	}
	return map[string]any{"status": true, "data": data}, nil
}

func (f *fakeAPI) Position() (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data := make([]any, len(f.positions))
	for i, r := range f.positions {
		data[i] = r
	}
	return map[string]any{"status": true, "data": data}, nil
}

func (f *fakeAPI) GTTCreateRule(p map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gttNext++
	id := fmt.Sprintf("GTT-%d", f.gttNext)
	f.gttMade = append(f.gttMade, id)
	return id, nil
}

func (f *fakeAPI) GTTCancelRule(p map[string]any) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.cancelCalls
	f.cancelCalls++
	if i < len(f.cancelErr) && f.cancelErr[i] != nil {
		return nil, f.cancelErr[i]
	}
	f.gttCancel = append(f.gttCancel, fmt.Sprint(p["id"]))
	return map[string]any{"status": true}, nil
}

// setRow upserts an order-book row by orderid.
func (f *fakeAPI) setRow(row map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.rows {
		if r["orderid"] == row["orderid"] {
			f.rows[i] = row
			return
		}
	}
	f.rows = append(f.rows, row)
}

func (f *fakeAPI) placedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.placed)
}

func (f *fakeAPI) placedAt(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.placed[i]
}

func (f *fakeAPI) cancelled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.gttCancel...)
}

// liveExecutor returns an executor wired to fake with a live session and
// fast polling so async paths finish within the test.
func liveExecutor(t *testing.T, fake *fakeAPI) *OrderExecutor {
	t.Helper()
	oldLookup, oldFill, oldEvery, oldTries := defaultLookupDelays, defaultFillPollDelays, asyncPollEvery, asyncPollTries
	defaultLookupDelays = []time.Duration{0}
	defaultFillPollDelays = []time.Duration{0}
	asyncPollEvery, asyncPollTries = time.Millisecond, 50
	oldBookMin := orderBookMinInterval
	orderBookMinInterval = 0
	t.Cleanup(func() { orderBookMinInterval = oldBookMin })
	t.Cleanup(func() {
		defaultLookupDelays, defaultFillPollDelays, asyncPollEvery, asyncPollTries = oldLookup, oldFill, oldEvery, oldTries
	})

	oe := NewOrderExecutor(Config{
		Qty:              75,
		CallFNOToken:     "111",
		CallFNOSymbol:    "NIFTY_CE",
		PutFNOToken:      "222",
		PutFNOSymbol:     "NIFTY_PE",
		FNOExchange:      "NFO",
		FNOOrderType:     "MARKET",
		FNOProductType:   "INTRADAY",
		FNOStopLossPaise: 1000,
		LogPrefix:        "[test]",
		RLMaxOrders:      100,
		RLWindow:         time.Minute,
	})
	oe.mu.Lock()
	oe.api = fake
	oe.live = true
	oe.sessionOK = true
	oe.mu.Unlock()
	return oe
}

func realSig(action strategy.Action) strategy.Signal {
	return strategy.Signal{StrategyName: "NIFTY50_FNO", Action: action, Side: strategy.SideCall, Token: "99926000", Exchange: "NSE", Qty: 75}
}

func entryFor(oe *OrderExecutor, s strategy.Signal) (OrderRecord, bool) {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	r, ok := oe.entryOrders[oe.positionKey(s)]
	return r, ok
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
