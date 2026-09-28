package strategy

import "testing"

func bar(o, h, l, c int64) ohlcv { return ohlcv{Open: o, High: h, Low: l, Close: c} }

func TestIsHammer(t *testing.T) {
	cases := []struct {
		name string
		b    ohlcv
		want bool
	}{
		{"long lower wick, small body near high", bar(10050, 10060, 9950, 10058), true},
		{"bearish body still counts as hammer", bar(10058, 10060, 9950, 10050), true},
		{"full body candle", bar(9950, 10060, 9950, 10060), false},
		{"long upper wick is shooting star, not hammer", bar(9960, 10060, 9950, 9955), false},
		{"zero range", bar(100, 100, 100, 100), false},
	}
	for _, tc := range cases {
		if got := isHammer(tc.b); got != tc.want {
			t.Errorf("%s: isHammer=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsShootingStar(t *testing.T) {
	if !isShootingStar(bar(9960, 10060, 9950, 9952)) {
		t.Error("long upper wick with body near low should be shooting star")
	}
	if isShootingStar(bar(10050, 10060, 9950, 10058)) {
		t.Error("hammer shape must not be shooting star")
	}
}

func TestEngulfing(t *testing.T) {
	prevBear := bar(10050, 10055, 9995, 10000)
	bullEngulf := bar(9995, 10070, 9990, 10060)
	if !isBullishEngulfing(prevBear, bullEngulf) {
		t.Error("expected bullish engulfing")
	}
	if isBullishEngulfing(prevBear, bar(10010, 10040, 10005, 10030)) {
		t.Error("body inside previous body is not engulfing")
	}
	prevBull := bar(10000, 10055, 9995, 10050)
	bearEngulf := bar(10055, 10060, 9980, 9990)
	if !isBearishEngulfing(prevBull, bearEngulf) {
		t.Error("expected bearish engulfing")
	}
	if isBearishEngulfing(prevBear, bearEngulf) {
		t.Error("previous candle must be bullish for bearish engulfing")
	}
}

func TestReversalHelpers(t *testing.T) {
	prevBear := bar(10050, 10055, 9995, 10000)
	if !bullishReversal(prevBear, bar(10050, 10060, 9950, 10058)) {
		t.Error("hammer is a bullish reversal")
	}
	prevBull := bar(10000, 10055, 9995, 10050)
	if !bearishReversal(prevBull, bar(9960, 10060, 9950, 9952)) {
		t.Error("shooting star is a bearish reversal")
	}
	flat := bar(10000, 10001, 9999, 10000)
	if bullishReversal(flat, flat) || bearishReversal(flat, flat) {
		t.Error("doji-sized flat bar is not a reversal")
	}
}
