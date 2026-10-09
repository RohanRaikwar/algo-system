package push

import (
	"strings"
	"testing"
)

func TestFormatSignal(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantOK    bool
		wantTitle string
		wantBody  []string // substrings
		wantTag   string
		sticky    bool
	}{
		{
			name:      "buy",
			payload:   `{"strategy_name":"NIFTY50_SR","action":"BUY","side":"CALL","entry_fno_price":12345,"fno_symbol":"NIFTY24500CE","fno_token":"111","order_mode":"PAPER","reason":"support bounce"}`,
			wantOK:    true,
			wantTitle: "BUY NIFTY50_SR [PAPER]",
			wantBody:  []string{"NIFTY24500CE @ ₹123.45", "support bounce"},
			wantTag:   "entry-111",
		},
		{
			name:      "buy without premium falls back to index",
			payload:   `{"strategy_name":"S","action":"BUY","side":"PUT","price":2450055}`,
			wantOK:    true,
			wantTitle: "BUY S",
			wantBody:  []string{"PUT, index ₹24,500.55"},
		},
		{
			name:      "exit with pnl",
			payload:   `{"strategy_name":"S","action":"EXIT","entry_fno_price":10000,"current_fno_price":8550,"fno_symbol":"X","fno_token":"9"}`,
			wantOK:    true,
			wantTitle: "EXIT S",
			wantBody:  []string{"X @ ₹85.50", "(-₹14.50/unit)"},
			wantTag:   "exit-9",
			sticky:    true,
		},
		{
			name:      "watch exit",
			payload:   `{"strategy_name":"S","action":"WATCH_EXIT","entry_fno_price":10000,"current_fno_price":12000,"fno_token":"9","reason":"theta"}`,
			wantOK:    true,
			wantTitle: "Exit advice S",
			wantBody:  []string{"(+₹20.00/unit)", "theta"},
			wantTag:   "watch-9",
			sticky:    true,
		},
		{name: "watch hold ignored", payload: `{"action":"WATCH_HOLD"}`},
		{name: "bad json", payload: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := FormatSignal([]byte(tt.payload))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if n.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", n.Title, tt.wantTitle)
			}
			for _, s := range tt.wantBody {
				if !strings.Contains(n.Body, s) {
					t.Errorf("body %q missing %q", n.Body, s)
				}
			}
			if tt.wantTag != "" && n.Tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", n.Tag, tt.wantTag)
			}
			if n.Sticky != tt.sticky {
				t.Errorf("sticky = %v, want %v", n.Sticky, tt.sticky)
			}
		})
	}
}

func refusedJSON(date string, ts ...string) []byte {
	var b strings.Builder
	b.WriteString(`{"date":"` + date + `","entries":[`)
	for i, s := range ts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"strategy":"S","side":"CALL","strike":24500,"reason":"kill switch","ts":"` + s + `"}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func TestRefusedTracker(t *testing.T) {
	var tr RefusedTracker

	if got := tr.Next(refusedJSON("2026-10-09", "a", "b")); len(got) != 0 {
		t.Fatalf("first message must only prime, got %d", len(got))
	}
	got := tr.Next(refusedJSON("2026-10-09", "a", "b", "c"))
	if len(got) != 1 || got[0].Title != "Refused S CALL" || got[0].Body != "strike 24500: kill switch" {
		t.Fatalf("one new entry: got %+v", got)
	}
	if got := tr.Next(refusedJSON("2026-10-09", "a", "b", "c")); len(got) != 0 {
		t.Fatalf("duplicate publish: got %d", len(got))
	}
	// Cap reached: oldest dropped, one added.
	if got := tr.Next(refusedJSON("2026-10-09", "b", "c", "d")); len(got) != 1 || got[0].Tag != "refused-d" {
		t.Fatalf("trimmed list: got %+v", got)
	}
	// New day: every entry is new.
	if got := tr.Next(refusedJSON("2026-10-10", "a")); len(got) != 1 {
		t.Fatalf("new day: got %d", len(got))
	}
}

func TestRupees(t *testing.T) {
	for p, want := range map[int64]string{
		0:         "₹0.00",
		5:         "₹0.05",
		12345:     "₹123.45",
		123456789: "₹1,234,567.89",
		-150:      "-₹1.50",
	} {
		if got := Rupees(p); got != want {
			t.Errorf("Rupees(%d) = %q, want %q", p, got, want)
		}
	}
}
