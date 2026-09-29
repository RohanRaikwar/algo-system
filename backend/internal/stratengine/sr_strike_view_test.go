package stratengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func strikeSelSvc(g *fakeGreeks, sent *[]string) *Service {
	svc := deltaSvc(g, 2269000, "CE22700", "PE22700")
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax = 0.45, 0.60
	svc.strikeSelPublishHook = func(p string) { *sent = append(*sent, p) }
	return svc
}

func lastStrikeSel(t *testing.T, sent []string) strikeSelView {
	t.Helper()
	if len(sent) == 0 {
		t.Fatal("nothing published")
	}
	var v strikeSelView
	if err := json.Unmarshal([]byte(sent[len(sent)-1]), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStrikeSelLivePickWithGreeksAndRejects(t *testing.T) {
	now := time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC) // 11:30 IST, DTE 7
	ce := contract(22700, "CE", 0.52)
	ce.Gamma, ce.Theta, ce.Vega, ce.IV, ce.LiquidityScore = 0.0011, -6.5, 11.2, 0.142, 250000
	g := &fakeGreeks{chain: []orderexec.OptionContract{ce, contract(22800, "CE", 0.30), contract(22700, "PE", -0.20)}}
	var sent []string
	svc := strikeSelSvc(g, &sent)

	svc.refreshStrikeSel(context.Background(), now)
	v := lastStrikeSel(t, sent)
	if v.Params.DeltaMin != 0.45 || v.Params.DeltaMax != 0.60 || v.Error != "" {
		t.Fatalf("params/error = %+v %q", v.Params, v.Error)
	}
	p := v.Call.Pick
	if p == nil || p.Strike != 22700 || p.Symbol != "NIFTY06OCT2622700CE" || p.Token != "CE22700" ||
		p.Delta != 0.52 || p.IV != 14.2 || p.Premium != 50 || p.DTE != 7 || p.Expiry != "2026-10-06" {
		t.Fatalf("call pick = %+v", p)
	}
	if v.Call.Rejects.Delta != 1 {
		t.Fatalf("call rejects = %+v", v.Call.Rejects)
	}
	if v.Put.Pick != nil || v.Put.Rejects.Delta != 1 {
		t.Fatalf("put = %+v", v.Put)
	}
}

func TestStrikeSelReportsMissingChain(t *testing.T) {
	var sent []string
	svc := strikeSelSvc(&fakeGreeks{err: errors.New("no such host")}, &sent)
	svc.refreshStrikeSel(context.Background(), time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC))
	v := lastStrikeSel(t, sent)
	if v.Error == "" || v.Call != nil || v.Put != nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestPickSRStrikeRecordsLastPick(t *testing.T) {
	now := time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC)
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(22650, "PE", -0.50)}}
	var sent []string
	svc := strikeSelSvc(g, &sent)
	svc.orderExecutor.UpdateLTP(model.Tick{Token: "PE22650", Exchange: "NFO", Price: 9000})
	sig := strategy.Signal{StrategyName: "NIFTY50_SR", Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 22700}
	if err := svc.pickSRStrike(&sig, now); err != nil {
		t.Fatal(err)
	}
	l := lastStrikeSel(t, sent).Last
	if l == nil || l.Strike != 22650 || l.AskedStrike != 22700 || l.Side != "PUT" || l.Premium != 90 || l.Delta != -0.50 {
		t.Fatalf("last = %+v", l)
	}
}
