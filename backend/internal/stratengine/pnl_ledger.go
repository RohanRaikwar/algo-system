// P&L trade ledger: every closing fill becomes a row in the SQLite ledger
// (internal/pnlstore, STRAT_PNL_DB_PATH), with the entry fill it closes,
// paper and real. Fills arrive on the order goroutine, so the writes go
// through a queue to one writer goroutine and never block an order.

package stratengine

import (
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/pnlstore"
	"trading-systemv1/internal/underlying"
)

// ledgerStore is what the ledger needs of the store; *pnlstore.Store
// satisfies it.
type ledgerStore interface {
	PutEntry(pnlstore.Entry) error
	DeleteEntry(key string) error
	Entries() ([]pnlstore.Entry, error)
	AddTrade(pnlstore.Trade) error
}

// ledgerQueue is the writer's buffer: far more than a day's fills.
const ledgerQueue = 1024

// pnlLedger keeps the open entries in memory (backed by the store) and
// queues the store writes.
type pnlLedger struct {
	mu      sync.Mutex
	entries map[string]pnlstore.Entry
	ops     chan func(ledgerStore) error
	done    chan struct{}
	closeMu sync.Once
}

// newPNLLedger loads the open entries and starts the writer.
func newPNLLedger(store ledgerStore) (*pnlLedger, error) {
	open, err := store.Entries()
	if err != nil {
		return nil, err
	}
	l := &pnlLedger{entries: make(map[string]pnlstore.Entry, len(open)), ops: make(chan func(ledgerStore) error, ledgerQueue), done: make(chan struct{})}
	for _, e := range open {
		l.entries[e.Key()] = e
	}
	go func() {
		defer close(l.done)
		for op := range l.ops {
			if err := op(store); err != nil {
				log.Printf("[stratengine] ⚠️  P&L ledger write: %v", err)
			}
		}
	}()
	return l, nil
}

// close drains the queue and stops the writer.
func (l *pnlLedger) close() {
	l.closeMu.Do(func() {
		close(l.ops)
		<-l.done
	})
}

// enqueue hands a write to the writer; a full queue drops it (logged)
// rather than block the order goroutine.
func (l *pnlLedger) enqueue(what string, op func(ledgerStore) error) {
	select {
	case l.ops <- op:
	default:
		log.Printf("[stratengine] 🚨 PNL LEDGER DROPPED %s (queue full)", what)
	}
}

// fillInfo is one fill as the ledger sees it.
type fillInfo struct {
	key        string // strategy|exchange:token|leg, the P&L tracker's position
	opening    bool
	strategy   string
	underlying string
	side       string
	leg        string
	short      bool
	exchange   string
	token      string // option traded
	symbol     string
	qty        int64
	price      int64
	realized   int64 // from the P&L tracker, for a closing fill
	reason     string
	real       bool
	at         time.Time
}

// record books one fill: an opening fill adds to its entry, a closing
// fill writes a trade row and reduces (or removes) the entry.
func (l *pnlLedger) record(f fillInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[f.key]
	if f.opening {
		if ok && e.Qty > 0 {
			// Added to an open position: average the entry.
			e.Price = (e.Price*e.Qty + f.price*f.qty) / (e.Qty + f.qty)
			e.Qty += f.qty
		} else {
			e = pnlstore.Entry{PosKey: f.key, Strategy: f.strategy, Underlying: f.underlying, Side: f.side, Leg: f.leg, Short: f.short,
				Exchange: f.exchange, Token: f.token, FNOSymbol: f.symbol, Qty: f.qty, Price: f.price, At: f.at, Real: f.real}
		}
		e.Real = e.Real || f.real
		l.entries[f.key] = e
		l.enqueue("entry "+f.key, func(s ledgerStore) error { return s.PutEntry(e) })
		return
	}

	t := pnlstore.Trade{Strategy: f.strategy, Underlying: f.underlying, Side: f.side, Leg: f.leg, Short: f.short,
		Exchange: f.exchange, Token: f.token, FNOSymbol: f.symbol, Qty: f.qty, ExitPrice: f.price,
		Realized: f.realized, ExitAt: f.at, ExitReason: f.reason, Real: f.real}
	if ok {
		// The entry knows the contract and side even when the exit
		// signal (a restart settle, an outside sale) does not.
		t.Underlying, t.Side, t.Exchange, t.Token, t.FNOSymbol = e.Underlying, e.Side, e.Exchange, e.Token, e.FNOSymbol
		t.EntryPrice, t.EntryAt, t.Real = e.Price, e.At, e.Real || f.real
		if left := e.Qty - f.qty; left > 0 {
			e.Qty = left
			l.entries[f.key] = e
			l.enqueue("entry "+f.key, func(s ledgerStore) error { return s.PutEntry(e) })
		} else {
			delete(l.entries, f.key)
			key := f.key
			l.enqueue("entry "+key, func(s ledgerStore) error { return s.DeleteEntry(key) })
		}
	}
	l.enqueue("trade "+f.key, func(s ledgerStore) error { return s.AddTrade(t) })
}

// ledgerFill builds the ledger's view of a fill. key/exchange/token are
// what onFill booked the P&L tracker on.
func (svc *Service) ledgerFill(f orderexec.FillReport, key, exchange, token string, qty, realized int64, opening bool) fillInfo {
	sig := f.Signal
	fi := fillInfo{key: key, opening: opening, strategy: sig.StrategyName, side: string(sig.Side), leg: sig.Leg,
		short: sig.Leg != "" && sig.Short, exchange: exchange, token: token, qty: qty, price: f.FillPricePaise,
		realized: realized, reason: sig.Reason, real: f.Real, at: time.Now()}
	prof, ok := underlying.ByStrategy(sig.StrategyName)
	if !ok {
		prof = underlying.Primary()
	}
	fi.underlying = prof.IndexKey()
	if sig.Leg == "" {
		// Single-leg fills are booked on the index; the ledger names the option.
		fi.token, fi.exchange = svc.unqualifyToken(sig.FNOToken), prof.FNOExchange
		if fi.token == "" {
			if lo, ok := svc.liveOrderPayload(sig.StrategyName, sig.Side); ok {
				fi.token = lo.FNOToken
			}
		}
	}
	fi.symbol = sig.FNOSymbol
	if fi.symbol == "" && fi.token != "" {
		fi.symbol = svc.fnoSymbolFor(fi.token)
	}
	return fi
}

// attachPNLLedger opens the ledger; without it trading goes on unrecorded.
func (svc *Service) attachPNLLedger() {
	path := svc.cfg.PNLDBPath
	if path == "" {
		log.Println("[stratengine] ⚪ P&L ledger disabled (STRAT_PNL_DB_PATH empty)")
		return
	}
	store, err := pnlstore.Open(path)
	if err != nil {
		log.Printf("[stratengine] ⚠️  P&L ledger %s: %v — trades will not be recorded", path, err)
		return
	}
	l, err := newPNLLedger(store)
	if err != nil {
		store.Close()
		log.Printf("[stratengine] ⚠️  P&L ledger %s: %v — trades will not be recorded", path, err)
		return
	}
	svc.pnlLedger, svc.pnlStore = l, store
	log.Printf("[stratengine] 📒 P&L ledger %s (%d open entries)", path, len(l.entries))
}

// closePNLLedger flushes queued writes and closes the store.
func (svc *Service) closePNLLedger() {
	if svc.pnlLedger != nil {
		svc.pnlLedger.close()
	}
	if svc.pnlStore != nil {
		svc.pnlStore.Close()
	}
}
