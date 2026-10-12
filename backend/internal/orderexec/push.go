package orderexec

import (
	"log"
	"sync"
	"time"
)

// pushBook holds terminal order statuses pushed by Angel's order-status
// socket and wakes code waiting on them. Only terminal statuses are kept:
// they are final, so a stored row can never go stale even if the socket
// drops later. Non-terminal pushes are ignored; the REST order book stays
// the source for those. The zero value is ready to use.
type pushBook struct {
	mu      sync.Mutex
	rows    map[string]pushedRow
	waiters map[string][]chan struct{}
}

type pushedRow struct {
	f  fillResult
	at time.Time
}

// pushKeep bounds the cache: rows older than this are dropped on insert.
const pushKeep = 24 * time.Hour

// put stores a terminal status for orderID and wakes its waiters. The first
// terminal status wins. It reports whether the row was new.
func (p *pushBook) put(orderID string, f fillResult) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rows == nil {
		p.rows = make(map[string]pushedRow)
	}
	if _, ok := p.rows[orderID]; ok {
		return false
	}
	now := time.Now()
	for id, r := range p.rows {
		if now.Sub(r.at) > pushKeep {
			delete(p.rows, id)
		}
	}
	p.rows[orderID] = pushedRow{f: f, at: now}
	for _, ch := range p.waiters[orderID] {
		close(ch)
	}
	delete(p.waiters, orderID)
	return true
}

// terminal returns the pushed terminal status of orderID, if any.
func (p *pushBook) terminal(orderID string) (fillResult, bool) {
	if p == nil || orderID == "" {
		return fillResult{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.rows[orderID]
	return r.f, ok
}

// wait sleeps up to d, returning early once orderID has a terminal push.
// With no orderID (or a nil book) it is a plain sleep.
func (p *pushBook) wait(orderID string, d time.Duration) {
	if d <= 0 {
		return
	}
	if p == nil || orderID == "" {
		time.Sleep(d)
		return
	}
	p.mu.Lock()
	if _, ok := p.rows[orderID]; ok {
		p.mu.Unlock()
		return
	}
	if p.waiters == nil {
		p.waiters = make(map[string][]chan struct{})
	}
	ch := make(chan struct{})
	p.waiters[orderID] = append(p.waiters[orderID], ch)
	p.mu.Unlock()

	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return
	case <-t.C:
	}
	p.mu.Lock()
	ws := p.waiters[orderID]
	for i, w := range ws {
		if w == ch {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(p.waiters, orderID)
	} else {
		p.waiters[orderID] = ws
	}
	p.mu.Unlock()
}

// HandleOrderUpdate takes one order update pushed by Angel's order-status
// socket (an orderData object; same keys as an order-book row). A terminal
// status is cached, wakes anyone waiting on that order, is journaled, and —
// when it is a held position's stop — books the stop at once instead of at
// the next sweep. It never places an order itself and never blocks.
func (oe *OrderExecutor) HandleOrderUpdate(row map[string]any) {
	orderID := toStr(row["orderid"])
	if orderID == "" {
		return
	}
	f := parseFillRow(row)
	if f.terminal() && !oe.pushes.put(orderID, f) {
		return // already seen
	}
	oe.journalFill(toStr(row["ordertag"]), orderID, f, "push")
	if !f.terminal() {
		return
	}
	log.Printf("%s 📨 push: order %s %s (filled %d @ %d)", oe.logPrefix(), orderID, f.Status, f.FilledQty, f.AvgPricePaise)

	oe.mu.RLock()
	var stopKey string
	for k, r := range oe.entryOrders {
		if r.Real && r.SLOrderID == orderID {
			stopKey = k
			break
		}
	}
	oe.mu.RUnlock()
	if stopKey != "" {
		go oe.sweepStop(stopKey, orderID)
	}
}

// sweepStop runs the protection sweep for one position after its stop
// (orderID) reported a terminal status. It does nothing if the position
// moved on meanwhile (an exit cancelled the stop, or a new stop replaced it).
func (oe *OrderExecutor) sweepStop(posKey, orderID string) {
	if !oe.stopMode() || !oe.liveSession() {
		return
	}
	kl := oe.keyLock(posKey)
	kl.Lock()
	rec, ok := oe.entry(posKey)
	if ok && rec.SLOrderID == orderID {
		oe.sweepOne(posKey)
	}
	kl.Unlock()
	if ok {
		oe.notifyState()
	}
}
