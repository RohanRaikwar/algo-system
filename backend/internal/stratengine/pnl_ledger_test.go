package stratengine

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/pnlstore"
	"trading-systemv1/internal/portfolio"
	"trading-systemv1/internal/strategy"
)

// ledgerSvc is a service with a real P&L tracker and a ledger on a temp DB.
func ledgerSvc(t *testing.T, path string) *Service {
	t.Helper()
	store, err := pnlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := newPNLLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{pnlTracker: portfolio.NewPnLTracker(), portfolio: portfolio.New(), pnlLedger: l, pnlStore: store}
}

func dayTrades(t *testing.T, path string) []pnlstore.Trade {
	t.Helper()
	s, err := pnlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Trades(time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPNLLedger_PaperRoundTrip(t *testing.T) {
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	path := filepath.Join(t.TempDir(), "pnl.db")
	svc := ledgerSvc(t, path)
	buy := strategy.Signal{StrategyName: "SENSEX_SR", Side: strategy.SideCall, Exchange: "BSE", Token: "99919000", FNOToken: "BFO:845123", Qty: 20}
	svc.onFill(orderexec.FillReport{Signal: buy, Direction: "BUY", FillPricePaise: 15000, Qty: 20})
	exit := buy
	exit.Reason = "MANUAL EXIT"
	svc.onFill(orderexec.FillReport{Signal: exit, Direction: "SELL", FillPricePaise: 16500, Qty: 20})
	svc.closePNLLedger()

	got := dayTrades(t, path)
	if len(got) != 1 {
		t.Fatalf("trades %+v", got)
	}
	tr := got[0]
	if tr.Strategy != "SENSEX_SR" || tr.Underlying != "BSE:99919000" || tr.Side != "CALL" || tr.Exchange != "BFO" || tr.Token != "845123" ||
		tr.Qty != 20 || tr.EntryPrice != 15000 || tr.ExitPrice != 16500 || tr.Realized != 1500*20 || tr.ExitReason != "MANUAL EXIT" || tr.Real {
		t.Fatalf("row %+v", tr)
	}
	if tr.EntryAt.IsZero() || tr.ExitAt.Before(tr.EntryAt) {
		t.Fatalf("times %v → %v", tr.EntryAt, tr.ExitAt)
	}
}

func TestPNLLedger_PartialExitRealFlagAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pnl.db")
	svc := ledgerSvc(t, path)
	sig := strategy.Signal{StrategyName: "NIFTY50_SR", Side: strategy.SidePut, Exchange: "NSE", Token: "99926000", FNOToken: "45002"}
	svc.onFill(orderexec.FillReport{Signal: sig, Direction: "BUY", FillPricePaise: 10000, Qty: 130, Real: true})
	svc.onFill(orderexec.FillReport{Signal: sig, Direction: "SELL", FillPricePaise: 9000, Qty: 65, Real: true})
	svc.closePNLLedger()

	// Restart: the tracker is new, the ledger still knows the entry. The
	// settle signal after a restart has no contract (signalFor).
	svc = ledgerSvc(t, path)
	if len(svc.pnlLedger.entries) != 1 {
		t.Fatalf("open entries after restart %+v", svc.pnlLedger.entries)
	}
	settle := strategy.Signal{StrategyName: "NIFTY50_SR", Side: strategy.SidePut, Exchange: "NSE", Token: "99926000"}
	svc.onFill(orderexec.FillReport{Signal: settle, Direction: "SELL", FillPricePaise: 11000, Qty: 65, Real: true})
	svc.closePNLLedger()

	got := dayTrades(t, path)
	if len(got) != 2 {
		t.Fatalf("trades %+v", got)
	}
	for _, tr := range got {
		if !tr.Real || tr.Qty != 65 || tr.EntryPrice != 10000 || tr.Token != "45002" || tr.Side != "PUT" {
			t.Fatalf("row %+v", tr)
		}
	}
	if got[0].Realized != -1000*65 {
		t.Fatalf("first exit realized %d", got[0].Realized)
	}
	s, _ := pnlstore.Open(path)
	defer s.Close()
	if open, _ := s.Entries(); len(open) != 0 {
		t.Fatalf("entry left after the last exit: %+v", open)
	}
}

// blockingStore never finishes a write, so the queue fills.
type blockingStore struct {
	release chan struct{}
	once    sync.Once
}

func (b *blockingStore) PutEntry(pnlstore.Entry) error      { <-b.release; return nil }
func (b *blockingStore) DeleteEntry(string) error           { <-b.release; return nil }
func (b *blockingStore) Entries() ([]pnlstore.Entry, error) { return nil, nil }
func (b *blockingStore) AddTrade(pnlstore.Trade) error      { <-b.release; return nil }

func TestPNLLedger_FullQueueNeverBlocks(t *testing.T) {
	store := &blockingStore{release: make(chan struct{})}
	l, err := newPNLLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < ledgerQueue*3; i++ {
			l.record(fillInfo{key: "k", opening: i%2 == 0, qty: 1, price: 100, at: time.Now()})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("record blocked on a full queue")
	}
	close(store.release)
	l.close()
}
