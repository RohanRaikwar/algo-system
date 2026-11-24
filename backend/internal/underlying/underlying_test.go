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
	if typ, ok := FNOExchTypeOf("BFO"); !ok || typ != 4 {
		t.Fatalf("FNOExchTypeOf failed")
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

func TestBANKNIFTYProfile(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000,BSE:99919000,NSE:99926009")
	all := All()
	if len(all) != 3 || all[0].Key != "NIFTY" || all[1].Key != "SENSEX" || all[2].Key != "BANKNIFTY" {
		t.Fatalf("All() = %v, want NIFTY, SENSEX, BANKNIFTY", all)
	}
	b := all[2]
	if b.IndexKey() != "NSE:99926009" || b.IndexSubscribeToken() != "1:99926009" ||
		b.OptionName != "BANKNIFTY" || b.FNOExchange != "NFO" || b.FNOExchType != 2 ||
		b.StrikeStep != 100 || b.LotFallback != 30 || b.FreezeQty != 600 ||
		b.ExpiryWeekday != time.Tuesday || !b.MonthlyExpiry || b.StrategyName != "BANKNIFTY_SR" ||
		!b.BrokerGreeks || b.Scale(1000) != 2200 {
		t.Fatalf("BANKNIFTY profile: %v", b)
	}
	if p, ok := ByStrategy("BANKNIFTY_SR"); !ok || p.Key != "BANKNIFTY" {
		t.Fatal("ByStrategy(BANKNIFTY_SR) failed")
	}
	if p, ok := ByIndexKey("nse:99926009"); !ok || p.Key != "BANKNIFTY" {
		t.Fatal("ByIndexKey failed")
	}
	if p, ok := ByIndexToken("99926009"); !ok || p.Key != "BANKNIFTY" {
		t.Fatal("ByIndexToken failed")
	}
	if typ, ok := FNOExchTypeOf("NFO"); !ok || typ != 2 {
		t.Fatal("FNOExchTypeOf(NFO) failed")
	}
	if Primary().MonthlyExpiry || Get("SENSEX").MonthlyExpiry {
		t.Fatal("NIFTY/SENSEX must stay weekly")
	}
}

func TestBANKNIFTYOverridesAndUnderlyingsForm(t *testing.T) {
	t.Setenv("INDEX_TOKEN", "")
	t.Setenv("UNDERLYINGS", "NIFTY,banknifty")
	t.Setenv("BANKNIFTY_LOT_SIZE", "35")
	t.Setenv("BANKNIFTY_FREEZE_QTY", "900")
	t.Setenv("BANKNIFTY_SCALE", "23/10")
	all := All()
	if len(all) != 2 || all[1].Key != "BANKNIFTY" {
		t.Fatalf("All() = %v", all)
	}
	if b := all[1]; b.LotFallback != 35 || b.FreezeQty != 900 || b.Scale(1000) != 2300 {
		t.Fatalf("overrides not applied: %v", b)
	}
	if p := Primary(); p.LotFallback == 35 || p.FreezeQty == 900 {
		t.Fatalf("BANKNIFTY env leaked into NIFTY: %v", p)
	}
}
