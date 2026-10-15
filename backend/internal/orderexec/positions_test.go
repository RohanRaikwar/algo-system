package orderexec

import "testing"

func brokerPositions(rows ...map[string]any) map[string]any {
	data := make([]any, len(rows))
	for i, r := range rows {
		data[i] = r
	}
	return map[string]any{"status": true, "data": data}
}

func pos(token, symbol, netqty string) map[string]any {
	return map[string]any{"symboltoken": token, "tradingsymbol": symbol, "exchange": "NFO", "netqty": netqty}
}

func TestComparePositions_Match(t *testing.T) {
	exp := map[string]expectedPosition{"NFO:111": {Symbol: "CE1", Qty: 75}}
	mm, err := comparePositions(exp, brokerPositions(pos("111", "CE1", "75")), "NFO")
	if err != nil || len(mm) != 0 {
		t.Fatalf("want no mismatch, got %+v err=%v", mm, err)
	}
}

func TestComparePositions_BrokerFlatButWeThinkOpen(t *testing.T) {
	exp := map[string]expectedPosition{"NFO:111": {Symbol: "CE1", Qty: 75}}
	mm, _ := comparePositions(exp, brokerPositions(pos("111", "CE1", "0")), "NFO")
	if len(mm) != 1 || mm[0].Expected != 75 || mm[0].Broker != 0 {
		t.Fatalf("want 75 vs 0 mismatch, got %+v", mm)
	}
}

func TestComparePositions_MissingRowMeansFlat(t *testing.T) {
	exp := map[string]expectedPosition{"NFO:111": {Symbol: "CE1", Qty: 75}}
	mm, _ := comparePositions(exp, map[string]any{"status": true, "data": nil}, "NFO")
	if len(mm) != 1 || mm[0].Broker != 0 {
		t.Fatalf("want mismatch vs flat, got %+v", mm)
	}
}

func TestComparePositions_UntrackedBrokerPosition(t *testing.T) {
	mm, _ := comparePositions(map[string]expectedPosition{}, brokerPositions(pos("222", "PE9", "-75"), pos("333", "X", "0")), "NFO")
	if len(mm) != 1 || mm[0].Token != "222" || mm[0].Broker != -75 || mm[0].Expected != 0 {
		t.Fatalf("want one untracked -75 on 222, got %+v", mm)
	}
}

func TestComparePositions_BadNetQty(t *testing.T) {
	if _, err := comparePositions(nil, brokerPositions(pos("111", "CE1", "abc")), "NFO"); err == nil {
		t.Fatal("want error on unparsable netqty")
	}
}

// NFO and BFO tokens are only unique within a segment: the same number on
// both is two positions, not one.
func TestComparePositions_KeysBySegment(t *testing.T) {
	exp := map[string]expectedPosition{"NFO:111": {Symbol: "NIFTYCE", Qty: 65}}
	bfo := map[string]any{"symboltoken": "111", "tradingsymbol": "SENSEXCE", "exchange": "BFO", "netqty": "20"}
	mm, _ := comparePositions(exp, brokerPositions(pos("111", "NIFTYCE", "65"), bfo), "NFO")
	if len(mm) != 1 || mm[0].Exchange != "BFO" || mm[0].Broker != 20 {
		t.Fatalf("want one untracked BFO 20, got %+v", mm)
	}
	// A row without exchange is the default segment.
	row := map[string]any{"symboltoken": "111", "tradingsymbol": "NIFTYCE", "netqty": "65"}
	if mm, _ := comparePositions(exp, brokerPositions(row), "NFO"); len(mm) != 0 {
		t.Fatalf("exchange-less row not matched: %+v", mm)
	}
}
