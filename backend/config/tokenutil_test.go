package config

import (
	"reflect"
	"testing"
)

func TestExchangeTypeName(t *testing.T) {
	tests := []struct {
		code      string
		wantName  string
		wantKnown bool
	}{
		{code: "1", wantName: "NSE", wantKnown: true},
		{code: "2", wantName: "NFO", wantKnown: true},
		{code: "3", wantName: "BSE", wantKnown: true},
		{code: "4", wantName: "BSE_FO", wantKnown: true},
		{code: "5", wantName: "MCX_FO", wantKnown: true},
		{code: "7", wantName: "NCX_FO", wantKnown: true},
		{code: "13", wantName: "CDE_FO", wantKnown: true},
		{code: "99", wantName: "NSE", wantKnown: false},
		{code: "", wantName: "NSE", wantKnown: false},
	}

	for _, tc := range tests {
		gotName, gotKnown := ExchangeTypeName(tc.code)
		if gotName != tc.wantName || gotKnown != tc.wantKnown {
			t.Fatalf("ExchangeTypeName(%q) = (%q,%v), want (%q,%v)",
				tc.code, gotName, gotKnown, tc.wantName, tc.wantKnown)
		}
	}
}

func TestParseSubscribeTokenKeys(t *testing.T) {
	got := ParseSubscribeTokenKeys("1:99926000,2:57710,13:123,99:555,invalid,3:")
	want := []string{
		"NSE:99926000",
		"NFO:57710",
		"CDE_FO:123",
		"NSE:555", // unknown exchange code falls back to NSE
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSubscribeTokenKeys mismatch:\n got:  %v\n want: %v", got, want)
	}
}

func TestParseSubscribeTokenKeys_Empty(t *testing.T) {
	if got := ParseSubscribeTokenKeys("  "); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}
