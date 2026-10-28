package tgsignal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const vikashCall = "📊 INDEX OPTIONS 📊\n\n🀄️ NIFTY 22350 PE 🀄️\n\n🔹ENTRY PRICE ➖ 50\n\n🎯Target 1 ⛔️ 60\n🎯Target 2 ⛔️ 80\n🎯Target 3 ⛔️ 100\n\n⛔️ STOP LOSS : PAID ✅"

const tciCall = "💠 SENSEX 29 OCT 74700 CE💠\n\n🏛📈 BUY ABOVE :- 280🔺\n\n📊 📉Current Price : 280🔺\n\n\nTarget.🔔 310//320//330++🟥\n\n☠️ Stoploss : PAID▶️"

func TestParseCallsWithoutStop(t *testing.T) {
	for _, c := range []struct {
		name, text string
		und        string
		strike     int64
		opt        string
		trigger    int64
		targets    []int64
	}{
		{"vikash", vikashCall, "NIFTY", 22350, "PE", 5000, []int64{6000, 8000, 10000}},
		{"tci", tciCall, "SENSEX", 74700, "CE", 28000, []int64{31000, 32000, 33000}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sig, err := Parse(c.text)
			if !errors.Is(err, ErrNoSL) {
				t.Fatalf("err %v, want ErrNoSL", err)
			}
			if sig.Underlying != c.und || sig.Strike != c.strike || sig.OptionType != c.opt || sig.TriggerPaise != c.trigger ||
				len(sig.Targets) != len(c.targets) || sig.SLPaise != 0 {
				t.Fatalf("parsed %+v", sig)
			}
			for i := range c.targets {
				if sig.Targets[i] != c.targets[i] {
					t.Fatalf("targets %v, want %v", sig.Targets, c.targets)
				}
			}

			got, err := withDefaultSL(sig, err, 10)
			if err != nil || got.SLPaise != c.trigger*90/100 || !got.SLDefault {
				t.Fatalf("default SL: %+v %v", got, err)
			}
			if _, err := withDefaultSL(sig, ErrNoSL, 0); !errors.Is(err, ErrNoSL) {
				t.Fatalf("pct 0 must keep the refusal, got %v", err)
			}
		})
	}
}

// A stop in the message wins over the default; a broken call is not
// rescued by it.
func TestDefaultSLOnlyForMissingStop(t *testing.T) {
	sig, err := Parse(realCall)
	if err != nil {
		t.Fatal(err)
	}
	got, err := withDefaultSL(sig, err, 10)
	if err != nil || got.SLPaise != 18200 || got.SLDefault {
		t.Fatalf("message stop replaced: %+v %v", got, err)
	}
	if _, err := Parse("BUY NIFTY 22350 PE\nTarget 60\nSL PAID"); err == nil || errors.Is(err, ErrNoSL) {
		t.Fatalf("call with no entry: %v, want a refusal other than ErrNoSL", err)
	}
	for _, noise := range []string{"55 ✅✅✅", "280🔥🔥", "📊280 To 400📊", "ACCOUNT HANDLING PROFIT", "Active 🔥🔥🔥"} {
		if _, err := Parse(noise); !errors.Is(err, ErrNotACall) {
			t.Errorf("%q read as %v", noise, err)
		}
	}
}

func TestLLMDefaultSL(t *testing.T) {
	r := llmWithDefaultSL(LLMResult{Kind: "new_call", Underlying: "SENSEX", Strike: 74700, OptionType: "CE", Trigger: "280", SL: ""}, 10)
	c := r.Call()
	if c.SLPaise != 25200 || !c.SLDefault {
		t.Fatalf("LLM call %+v", c)
	}
	if r := llmWithDefaultSL(LLMResult{Kind: "new_call", Trigger: "280", SL: "260"}, 10); r.Call().SLPaise != 26000 || r.Call().SLDefault {
		t.Fatalf("LLM stop replaced: %+v", r.Call())
	}
	// Regex and LLM both default the stop: they still agree.
	rx, rxErr := Parse(tciCall)
	rx, rxErr = withDefaultSL(rx, rxErr, 10)
	d := decide(rx, rxErr, r, nil)
	if d.verdict != VerdictSent || d.call == nil || d.call.SLPaise != 25200 || !d.call.SLDefault {
		t.Fatalf("decide %+v", d)
	}
}

// The TCI channel's real history: every structured call reads (stop
// missing), nothing else does.
func TestGoldenTCI(t *testing.T) {
	f, err := os.Open("testdata/tci.jsonl")
	if err != nil {
		t.Skip("no golden set:", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	calls := 0
	for sc.Scan() {
		var m HistoryMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		up := strings.ToUpper(m.Text)
		sig, err := Parse(m.Text)
		structured := strings.Contains(up, "BUY ABOVE") && strings.Contains(up, "STOPLOSS : PAID")
		switch {
		case structured && strings.Contains(up, "BANKNIFTY"):
			if err == nil || errors.Is(err, ErrNoSL) {
				t.Errorf("#%d BANKNIFTY call accepted: %v", m.MsgID, err)
			}
		case structured:
			if !errors.Is(err, ErrNoSL) || sig.TriggerPaise <= 0 || len(sig.Targets) == 0 {
				t.Errorf("#%d call not read: %+v %v\n%s", m.MsgID, sig, err, m.Text)
				continue
			}
			calls++
		case err == nil || errors.Is(err, ErrNoSL):
			t.Errorf("#%d read as a call but is not one: %+v\n%s", m.MsgID, sig, m.Text)
		}
	}
	if calls < 10 {
		t.Errorf("read %d TCI calls, want at least 10", calls)
	}
}

func TestActiveReply(t *testing.T) {
	for text, want := range map[string]bool{"Active 🔥🔥🔥": true, "ACTIVE": true, "🔥 active now": true, "not active": false, "Inactive": false, "Active": true,
		"Active hone ka wait karna": false, "Active hone ka wait karna or sideways market h": false, "ACTIVE ✅✅": true} {
		if got := reActive.MatchString(text); got != want {
			t.Errorf("%q active=%v, want %v", text, got, want)
		}
	}
	got := Explain(context.Background(), nil, "Active 🔥🔥🔥", vikashCall, true, 10)
	if !strings.HasPrefix(got, VerdictFollowUp+" activate") {
		t.Fatalf("explain %q", got)
	}
	if got := Explain(context.Background(), nil, "Active 🔥🔥🔥", "Good morning traders", true, 10); strings.Contains(got, "activate") {
		t.Fatalf("Active to a non-call read as activate: %q", got)
	}
}
