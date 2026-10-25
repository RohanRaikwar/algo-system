package tgsignal

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// The group's real history (tghistory -dump): every structured call reads
// correctly, nothing else reads as a call, and the ambiguous two-contract
// "levels for tomorrow" messages are refused.
func TestGoldenIndianOperator(t *testing.T) {
	f, err := os.Open("testdata/indian_operator.jsonl")
	if err != nil {
		t.Skip("no golden set:", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	calls, total := 0, 0
	for sc.Scan() {
		var m HistoryMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		total++
		sig, err := Parse(m.Text)
		structured := strings.Contains(m.Text, "INTRADAY") && strings.Contains(strings.ToUpper(m.Text), "BUY")
		switch {
		case m.MsgID == 171 || m.MsgID == 414:
			if err == nil || errors.Is(err, ErrNotACall) {
				t.Errorf("#%d two-contract message: got %+v %v, want a refusal", m.MsgID, sig, err)
			}
		case structured:
			if err != nil {
				t.Errorf("#%d call not read: %v\n%s", m.MsgID, err, m.Text)
				continue
			}
			calls++
			if len(sig.Targets) == 0 {
				t.Errorf("#%d no targets read: %+v", m.MsgID, sig)
			}
		case err == nil:
			t.Errorf("#%d read as a call but is not one: %+v\n%s", m.MsgID, sig, m.Text)
		}
	}
	if calls != 16 {
		t.Errorf("read %d structured calls of %d messages, want 16", calls, total)
	}
}

// Spot checks of values read from real calls.
func TestGoldenValues(t *testing.T) {
	for _, c := range []struct {
		text         string
		strike       int64
		opt          string
		trig, sl, t1 int64
	}{
		{"F&O | ⚡ INTRADAY\n\n🟢 BUY SENSEX PE 77600\n\n💰 Entry: Near Above 255.00\n🎯 Target:1st: ₹301 .00\n🛑 Stop Loss: ₹234 .00", 77600, "PE", 25500, 23400, 30100},
		{"F&O | ⚡ INTRADAY\n\n🟢 BUY NIFTY CE 24100\n\n💰 Entry: Near Above 81.00\n🎯 Target: ₹89.00 , 105.00\n🛑 Stop Loss: ₹76.00", 24100, "CE", 8100, 7600, 8900},
		{"BUY NIFTY 24250 CE above 90 SL 15 point target 100", 24250, "CE", 9000, 7500, 10000},
	} {
		s, err := Parse(c.text)
		if err != nil {
			t.Fatalf("%q: %v", c.text, err)
		}
		if s.Strike != c.strike || s.OptionType != c.opt || s.TriggerPaise != c.trig || s.SLPaise != c.sl || s.Targets[0] != c.t1 {
			t.Errorf("%q: got %+v", c.text, s)
		}
	}
}
