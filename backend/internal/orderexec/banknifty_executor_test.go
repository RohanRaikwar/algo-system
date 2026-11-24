package orderexec

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// BANKNIFTY options trade on NFO, the same segment as NIFTY's, so these
// tests prove the symbol prefix (not the segment) picks its lot, freeze
// quantity and guard, and that NIFTY orders are unchanged.
const bnfSym, bnfTok = "BANKNIFTY30DEC2657000CE", "45123"

// bnfSig is a BANKNIFTY_SR entry on a resolved NFO contract.
func bnfSig(action model.Action, side model.PositionSide) model.Signal {
	return model.Signal{StrategyName: "BANKNIFTY_SR", Action: action, Side: side,
		Token: "99926009", Exchange: "NSE", FNOToken: bnfTok, FNOSymbol: bnfSym}
}

func bnfLive(t *testing.T, f *fakeAPI, live ...string) *OrderExecutor {
	t.Helper()
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000,BSE:99919000,NSE:99926009")
	oe := liveExecutor(t, f)
	oe.lotSizeOf = func(sym string) (int64, error) {
		switch {
		case strings.HasPrefix(sym, "BANKNIFTY"):
			return 30, nil
		case strings.HasPrefix(sym, "SENSEX"):
			return 20, nil
		}
		return 75, nil
	}
	for _, s := range live {
		oe.liveStrategies[s] = true
	}
	return oe
}

func TestExtraProfileOf_PrefixesDoNotCollide(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000,BSE:99919000,NSE:99926009")
	for sym, want := range map[string]string{
		"NIFTY30DEC2626000CE":     "",
		"BANKNIFTY30DEC2657000PE": "BANKNIFTY",
		"SENSEX26N0564600CE":      "SENSEX",
	} {
		p, ok := extraProfileOf(sym)
		if got := map[bool]string{true: p.Key}[ok]; got != want {
			t.Errorf("extraProfileOf(%s) = %q, want %q", sym, got, want)
		}
	}
	if underlyingOfStrategy("BANKNIFTY_SR") != "BANKNIFTY" || underlyingOfStrategy("NIFTY50_SR") != "NIFTY" {
		t.Fatal("underlyingOfStrategy")
	}
}

// A real BANKNIFTY entry goes to NFO, sized in BANKNIFTY lots, and its stop too.
func TestBankNifty_RealEntryAndStopOnNFO(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 400.0}
	oe := bnfLive(t, f, "BANKNIFTY_SR")
	oe.cfg.ProtectionMode = ProtectionStop
	oe.ExecuteSignal(bnfSig(model.ActionBuy, model.SideCall))
	if n := f.placedCount(); n < 1 {
		t.Fatalf("no real BUY placed")
	}
	buy := f.placedAt(0)
	if buy["exchange"] != "NFO" || buy["tradingsymbol"] != bnfSym || buy["quantity"] != "30" {
		t.Fatalf("BUY params = %v", buy)
	}
	waitFor(t, "protective stop", func() bool { return len(f.placedStops()) == 1 })
	if st := f.placedStops()[0]; st["exchange"] != "NFO" || st["quantity"] != "30" || st["tradingsymbol"] != bnfSym {
		t.Fatalf("stop params = %v", st)
	}
}

// An unknown BANKNIFTY lot size refuses the real entry.
func TestBankNifty_UnknownLotRefused(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 400.0}
	oe := bnfLive(t, f, "BANKNIFTY_SR")
	oe.lotSizeOf = noLotSize
	oe.ExecuteSignal(bnfSig(model.ActionBuy, model.SideCall))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d with unknown lot size", n)
	}
}

// NIFTY orders keep their lot and freeze when BANKNIFTY is configured.
func TestBankNifty_NiftyOrdersUnchanged(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := bnfLive(t, f)
	oe.ExecuteSignal(realSig(model.ActionBuy))
	if n := f.placedCount(); n < 1 {
		t.Fatalf("no NIFTY BUY")
	}
	if p := f.placedAt(0); p["exchange"] != "NFO" || p["quantity"] != "75" {
		t.Fatalf("NIFTY BUY params = %v", p)
	}
	if q := oe.freezeQtyFor("NIFTY30DEC2626000CE"); q != oe.cfg.FreezeQty {
		t.Fatalf("NIFTY freeze %d, want %d", q, oe.cfg.FreezeQty)
	}
}

// BANKNIFTY_SR off the allowlist is paper, sized in BANKNIFTY lots.
func TestBankNifty_PaperWhenNotAllowlisted(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 400.0}
	oe := bnfLive(t, f)
	s := bnfSig(model.ActionBuy, model.SideCall)
	oe.ExecuteSignal(s)
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d real orders for paper BANKNIFTY_SR", n)
	}
	e, ok := entryFor(oe, s)
	if !ok || e.Real || !strings.HasPrefix(e.OrderID, "PAPER_") || e.Quantity != 30 {
		t.Fatalf("paper entry = %+v ok=%v", e, ok)
	}
}

// BANKNIFTY's own freeze quantity applies.
func TestBankNifty_FreezeQty(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 400.0}
	oe := bnfLive(t, f, "BANKNIFTY_SR")
	oe.cfg.LiveLots = 21 // 21 × 30 = 630 > BANKNIFTY freeze 600 (< NIFTY's 1800)
	oe.ExecuteSignal(bnfSig(model.ActionBuy, model.SideCall))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d above BANKNIFTY freeze qty", n)
	}
}

// A real NIFTY CALL does not block a real BANKNIFTY PUT, though both are NFO.
func TestBankNifty_OppositeGuardPerUnderlying(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := bnfLive(t, f, "BANKNIFTY_SR")
	oe.ExecuteSignal(realSig(model.ActionBuy)) // NIFTY50_FNO CALL
	before := f.placedCount()
	oe.ExecuteSignal(bnfSig(model.ActionBuy, model.SidePut))
	waitFor(t, "BANKNIFTY PUT", func() bool {
		for i := before; i < f.placedCount(); i++ {
			if p := f.placedAt(i); p["tradingsymbol"] == bnfSym && p["transactiontype"] == "BUY" {
				return true
			}
		}
		return false
	})
}

// A real exit modifies the BANKNIFTY stop instead of selling again.
func TestBankNifty_ExitModifiesStop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 400.0}
	oe := bnfLive(t, f, "BANKNIFTY_SR")
	oe.cfg.ProtectionMode = ProtectionStop
	oe.cfg.ExitModifiesStop = true
	s := bnfSig(model.ActionBuy, model.SideCall)
	oe.ExecuteSignal(s)
	waitFor(t, "protective stop", func() bool { return len(f.placedStops()) == 1 })
	e, _ := entryFor(oe, s)
	oe.ExecuteSignal(bnfSig(model.ActionExit, model.SideCall))
	m := f.modifies()
	if len(m) != 1 || m[0]["orderid"] != e.SLOrderID {
		t.Fatalf("modifies %v, want one on stop %s", m, e.SLOrderID)
	}
	for i := 0; i < f.placedCount(); i++ {
		if p := f.placedAt(i); p["transactiontype"] == "SELL" && p["variety"] != stopVariety {
			t.Fatalf("extra SELL placed: %v", p)
		}
	}
	if _, ok := entryFor(oe, s); ok {
		t.Fatal("position still open after exit")
	}
}

// Reconcile with NIFTY and BANKNIFTY both on NFO books neither as the other.
func TestBankNifty_ReconcileSharedSegment(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 400.0}
	oe := bnfLive(t, f, "BANKNIFTY_SR")
	s := bnfSig(model.ActionBuy, model.SideCall)
	oe.ExecuteSignal(s)
	waitFor(t, "entry settled", func() bool {
		e, ok := entryFor(oe, s)
		return ok && e.Real && !e.Pending
	})
	f.mu.Lock()
	f.positions = []map[string]any{
		{"symboltoken": bnfTok, "tradingsymbol": bnfSym, "exchange": "NFO", "netqty": "30"},
	}
	f.mu.Unlock()
	mm, err := oe.ReconcilePositions()
	if err != nil || len(mm) != 0 {
		t.Fatalf("mismatches %+v err %v", mm, err)
	}
	// An untracked NIFTY position on NFO is unknown, not ours.
	f.mu.Lock()
	f.positions = append(f.positions, map[string]any{
		"symboltoken": "777", "tradingsymbol": "NIFTY30DEC2626000CE", "exchange": "NFO", "netqty": "75"})
	f.mu.Unlock()
	mm, _ = oe.ReconcilePositions()
	if len(mm) != 1 {
		t.Fatalf("want one unknown NIFTY position, got %+v", mm)
	}
}

// NIFTY50_SR and BANKNIFTY_SR firing on the same candle claim different intents.
func TestBankNifty_EntryKeyDistinctFromNifty(t *testing.T) {
	ts := time.Date(2026, 12, 1, 10, 15, 0, 0, time.UTC)
	n := model.Signal{StrategyName: "NIFTY50_SR", Action: model.ActionBuy, Side: model.SideCall, Token: "99926000", SignalTS: ts}
	b := bnfSig(model.ActionBuy, model.SideCall)
	b.SignalTS = ts
	if generateIdempotencyKey(n) == generateIdempotencyKey(b) {
		t.Fatal("same entry key for NIFTY50_SR and BANKNIFTY_SR on one candle")
	}
	if generateIdempotencyKey(b) != generateIdempotencyKey(b) {
		t.Fatal("BANKNIFTY entry key not deterministic")
	}
}
