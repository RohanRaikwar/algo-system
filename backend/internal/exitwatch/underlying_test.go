package exitwatch

import "testing"

func TestModelFor_ScalesSENSEXOnly(t *testing.T) {
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	m := Model{Params: DefaultParams()}
	e := NewEngine(m, nil, true)
	if got := e.modelFor("99926000"); got != &e.model {
		t.Fatalf("NIFTY got a scaled model")
	}
	sx := e.modelFor("99919000")
	if sx.RejectMinPaise != 1650 || sx.EarlyFailMinPaise != 2640 || sx.LevelMergeTolPaise != 1650 {
		t.Fatalf("SENSEX model not scaled: reject=%d early=%d merge=%d", sx.RejectMinPaise, sx.EarlyFailMinPaise, sx.LevelMergeTolPaise)
	}
	if sx.ExitP != e.model.ExitP || e.model.RejectMinPaise != 500 {
		t.Fatalf("scale-free fields changed or base mutated")
	}
	// A reload rescales the SENSEX model in place.
	m2 := m
	m2.RejectMinPaise = 1000
	e.SetModel(m2)
	if e.modelFor("99919000").RejectMinPaise != 3300 {
		t.Fatalf("reload not rescaled: %d", e.modelFor("99919000").RejectMinPaise)
	}
}

func TestParseOurSRXLevels(t *testing.T) {
	sets, err := parseOurSRXLevels([]byte(`{"BSE:99919000":{"key":"BSE:99919000","levels":[{"price":8420000,"source":"pdh"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	ls := sets["99919000"]
	if len(ls) != 1 || ls[0].Price != 8420000 || ls[0].Type != ourSRPrefix+"pdh" {
		t.Fatalf("levels = %+v", sets)
	}
}
