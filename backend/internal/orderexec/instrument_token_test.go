package orderexec

import "testing"

func TestSymbolForToken(t *testing.T) {
	im := &InstrumentMaster{}
	im.setInstrumentsLocked([]Instrument{
		{Token: "45678", Symbol: "NIFTY06OCT2622500PE", ExchSeg: "NFO"},
		{Token: "45678", Symbol: "SOMEEQ-EQ", ExchSeg: "NSE"},
	})
	if got := im.SymbolForToken("NFO", "45678"); got != "NIFTY06OCT2622500PE" {
		t.Fatalf("NFO symbol = %q", got)
	}
	if got := im.SymbolForToken("NSE", "45678"); got != "SOMEEQ-EQ" {
		t.Fatalf("tokens are per segment, got %q", got)
	}
	if got := im.SymbolForToken("NFO", "1"); got != "" {
		t.Fatalf("unknown token = %q", got)
	}
	if got := (&InstrumentMaster{}).SymbolForToken("NFO", "45678"); got != "" {
		t.Fatalf("unloaded master = %q", got)
	}
}
