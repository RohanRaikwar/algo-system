package underlying

import (
	"testing"
	"time"
)

func TestAll_DefaultIsNIFTYOnly(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "")
	all := All()
	if len(all) != 1 || all[0].Key != "NIFTY" {
		t.Fatalf("All() = %v, want NIFTY only", all)
	}
}

// NIFTY's profile must equal the literals the code used before profiles.
func TestNIFTYMatchesLegacyLiterals(t *testing.T) {
	for _, k := range []string{"NIFTY_LOT_SIZE", "STRAT_FREEZE_QTY", "STRAT_FNO_EXCHANGE"} {
		t.Setenv(k, "")
	}
	p := Primary()
	if p.IndexKey() != "NSE:99926000" || p.IndexSubscribeToken() != "1:99926000" ||
		p.OptionName != "NIFTY" || p.FNOExchange != "NFO" || p.FNOExchType != 2 ||
		p.StrikeStep != 50 || p.LotFallback != 65 || p.FreezeQty != 1800 ||
		p.ExpiryWeekday != time.Tuesday || p.StrategyName != "NIFTY50_SR" || p.Scale(1234) != 1234 {
		t.Fatalf("NIFTY profile changed: %v", p)
	}
}

func TestLegacyEnvNamesApplyToNIFTY(t *testing.T) {
	t.Setenv("NIFTY_LOT_SIZE", "75")
	t.Setenv("STRAT_FREEZE_QTY", "1755")
	t.Setenv("STRAT_FNO_EXCHANGE", "NFO")
	p := Primary()
	if p.LotFallback != 75 || p.FreezeQty != 1755 {
		t.Fatalf("legacy env not applied: %v", p)
	}
	if s := Get("SENSEX"); s.LotFallback != 20 || s.FreezeQty != 1000 {
		t.Fatalf("NIFTY env leaked into SENSEX: %v", s)
	}
}

func TestSENSEXProfileAndOverrides(t *testing.T) {
	t.Setenv("UNDERLYINGS", "nifty, sensex, bogus")
	t.Setenv("SENSEX_SCALE", "31/10")
	t.Setenv("SENSEX_EXPIRY_WEEKDAY", "tue")
	all := All()
	if len(all) != 2 || all[1].Key != "SENSEX" {
		t.Fatalf("All() = %v", all)
	}
	s := all[1]
	if s.IndexKey() != "BSE:99919000" || s.IndexSubscribeToken() != "3:99919000" ||
		s.FNOExchange != "BFO" || s.FNOExchType != 4 || s.StrategyName != "SENSEX_SR" {
		t.Fatalf("SENSEX profile wrong: %v", s)
	}
	if s.Scale(1000) != 3100 || s.ExpiryWeekday != time.Tuesday {
		t.Fatalf("overrides not applied: %v", s)
	}
	if p, ok := ByStrategy("SENSEX_SR"); !ok || p.Key != "SENSEX" {
		t.Fatalf("ByStrategy failed")
	}
	if p, ok := ByIndexKey("bse:99919000"); !ok || p.Key != "SENSEX" {
		t.Fatalf("ByIndexKey failed")
	}
	if p, ok := ByFNOExchange("BFO"); !ok || p.Key != "SENSEX" {
		t.Fatalf("ByFNOExchange failed")
	}
	if p, ok := ByIndexToken("99926000"); !ok || p.Key != "NIFTY" {
		t.Fatalf("ByIndexToken failed")
	}
}

func TestSENSEXNotConfiguredIsNotFound(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	if _, ok := ByStrategy("SENSEX_SR"); ok {
		t.Fatalf("SENSEX found without UNDERLYINGS")
	}
}

// SENSEX is added by listing its index in INDEX_TOKEN.
func TestAll_FromIndexTokenList(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000, bse:99919000 ,NSE:12345")
	all := All()
	if len(all) != 2 || all[0].Key != "NIFTY" || all[1].Key != "SENSEX" {
		t.Fatalf("All() = %v, want NIFTY, SENSEX", all)
	}
	// Both forms together: no duplicate.
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	if n := len(All()); n != 2 {
		t.Fatalf("duplicate profiles: %d", n)
	}
	if _, ok := ByStrategy("SENSEX_SR"); !ok {
		t.Fatal("SENSEX_SR not found")
	}
}
