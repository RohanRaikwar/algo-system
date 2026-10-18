package pnlstore

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreEntriesAndTrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pnl.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	e := Entry{PosKey: "SENSEX_SR|BSE:99919000|", Strategy: "SENSEX_SR", Underlying: "BSE:99919000", Side: "CALL", Exchange: "BFO", Token: "845123",
		FNOSymbol: "SENSEX26O2282000CE", Qty: 20, Price: 15050, At: at}
	if err := s.PutEntry(e); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopened: the open entry survives.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Entries()
	if err != nil || len(got) != 1 || got[0] != e {
		t.Fatalf("entries %+v %v", got, err)
	}
	if err := s.DeleteEntry(e.Key()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Entries(); len(got) != 0 {
		t.Fatal("entry not deleted")
	}

	for i, strat := range []string{"SENSEX_SR", "NIFTY50_SR", "SENSEX_SR"} {
		tr := Trade{Strategy: strat, Side: "CALL", Qty: 20, EntryPrice: 15050, ExitPrice: 16000, Realized: 19000,
			EntryAt: at, ExitAt: at.Add(time.Duration(i+1) * time.Hour), ExitReason: "TARGET", Real: i == 2}
		if err := s.AddTrade(tr); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.Trades(at, at.Add(24*time.Hour), "")
	if len(all) != 3 || all[0].ExitAt != at.Add(time.Hour) || all[0].Realized != 19000 {
		t.Fatalf("all %+v", all)
	}
	sx, _ := s.Trades(at, at.Add(24*time.Hour), "SENSEX_SR")
	if len(sx) != 2 || !sx[1].Real || sx[0].Real {
		t.Fatalf("sensex %+v", sx)
	}
	if w, _ := s.Trades(at.Add(90*time.Minute), at.Add(150*time.Minute), ""); len(w) != 1 || w[0].Strategy != "NIFTY50_SR" {
		t.Fatalf("window %+v", w)
	}
	if first, ok, err := s.FirstExit(); err != nil || !ok || !first.Equal(at.Add(time.Hour)) {
		t.Fatalf("first %v %v %v", first, ok, err)
	}

	r, err := OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got, _ := r.Trades(at, at.Add(24*time.Hour), ""); len(got) != 3 {
		t.Fatalf("reader %d", len(got))
	}
	if err := r.AddTrade(Trade{Strategy: "X", ExitAt: at}); err == nil {
		t.Fatal("reader wrote")
	}
}

func TestFirstExitEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "pnl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok, err := s.FirstExit(); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
