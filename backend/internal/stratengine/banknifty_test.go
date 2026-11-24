package stratengine

import (
	"testing"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/internal/underlying"
)

// BANKNIFTY is a third underlying whose options share NFO with NIFTY's.
func TestSRExtras_BankNiftyBuiltFromProfile(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000,BSE:99919000,NSE:99926009")
	t.Setenv("STRAT_SR_BANKNIFTY_MAX_DAY_LOSS_PAISE", "")
	svc := &Service{
		cfg:           Config{Qty: 65, SRDeltaMin: 0.45, SRDeltaMax: 0.60},
		orderExecutor: orderexec.NewOrderExecutor(orderexec.Config{Qty: 65, FNOExchange: "NFO", LogPrefix: "[test]"}),
	}
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())
	extras := svc.buildSRExtras(strategy.DefaultNifty50SRConfig())
	if len(extras) != 2 || extras[0].prof.Key != "SENSEX" || extras[1].prof.Key != "BANKNIFTY" {
		t.Fatalf("extras = %d", len(extras))
	}
	ex := extras[1]
	c := ex.strat.Config()
	if ex.strat.Name() != "BANKNIFTY_SR" || c.IndexToken != "NSE:99926009" || c.StrikeStep != 100 {
		t.Fatalf("BANKNIFTY SR config: name=%s index=%s step=%d", ex.strat.Name(), c.IndexToken, c.StrikeStep)
	}
	nifty := strategy.DefaultNifty50SRConfig()
	if c.TouchTolPts != nifty.TouchTolPts*22/10 || c.MaxDayLossPts != nifty.MaxDayLossPts*22/10 {
		t.Fatalf("points not scaled: touch=%d maxloss=%d", c.TouchTolPts, c.MaxDayLossPts)
	}
	if ex.snapshotKey() != "banknifty_sr" || svc.srByName("BANKNIFTY_SR") != ex.strat || len(svc.srAll()) != 3 {
		t.Fatalf("lookup: key=%s all=%d", ex.snapshotKey(), len(svc.srAll()))
	}
	if got := svc.qualifyFNOTokenFor("BANKNIFTY_SR", "45123"); got != "NFO:45123" {
		t.Fatalf("qualify BANKNIFTY = %s", got)
	}
	if got := svc.qualifyFNOTokenFor("SENSEX_SR", "1163095"); got != "BFO:1163095" {
		t.Fatalf("qualify SENSEX = %s", got)
	}
	// Each underlying keeps its own picker; NIFTY's picker is not shared.
	if ex.picker == nil || ex.picker == extras[0].picker {
		t.Fatal("BANKNIFTY picker missing or shared")
	}
	if tgExchType("NFO") != 2 || tgExchType("BFO") != 4 {
		t.Fatal("tgExchType")
	}
}

// A BANKNIFTY TG call routes to the BANKNIFTY index and its lot size.
func TestTGBankNiftyIndexAndQty(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000,NSE:99926009")
	svc, _ := tgService(&offlineFake{})
	svc.cfg.Qty, svc.cfg.LiveLots = 65, 2
	svc.srExtras = svc.buildSRExtras(strategy.DefaultNifty50SRConfig())
	if tok, ex := svc.tgIndexOf("BANKNIFTY"); tok != "99926009" || ex != "NSE" {
		t.Fatalf("BANKNIFTY = %s:%s", ex, tok)
	}
	if tok, _ := svc.tgIndexOf("NIFTY"); tok != "99926000" {
		t.Fatalf("NIFTY = %s", tok)
	}
	if got := svc.tgQtyOf(underlying.Get("BANKNIFTY")); got != 60 {
		t.Fatalf("BANKNIFTY qty %d, want 2 lots × 30", got)
	}
}
