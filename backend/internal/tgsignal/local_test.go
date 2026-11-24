package tgsignal

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestLocalFormats(t *testing.T) {
	for _, c := range []struct {
		re   string
		text string
		want bool
	}{
		{"active", "Active ho gaya 🔥", true},
		{"active", "Entry active", true},
		{"active", "Entry done ✅", true},
		{"active", "Abhi le lo", true},
		{"active", "entry le lo abhi", true},
		{"active", "Abi active nhi hua h wait karna", false},
		{"active", "Active hone ka wait karna", false},
		{"active", "Entry le lo abhi, market strong hai", false},
		{"active", "done", false},
		{"ctc", "Sl ctc kar lena", true},
		{"ctc", "SL cost", true},
		{"ctc", "ctc sl", true},
		{"ctc", "Sl ctc kar lena, safe traders exit", false},
		{"ctc", "Abi ctc h", false},
		{"cancel", "Avoid", true},
		{"cancel", "cancel ❌", true},
		{"cancel", "avoid karo", true},
		{"cancel", "avoid gold today", false},
	} {
		var got bool
		switch c.re {
		case "active":
			got = reActive.MatchString(c.text)
		case "ctc":
			got = reLocalCTC.MatchString(c.text)
		case "cancel":
			got = reLocalCancel.MatchString(c.text)
		}
		if got != c.want {
			t.Errorf("%s %q = %v, want %v", c.re, c.text, got, c.want)
		}
	}
	for text, want := range map[string]int64{"sl 112 par le aao": 11200, "SL 195 pe kar lena": 19500, "sl 112": 0, "sl 112 par le aao bhai sab log": 0} {
		if got := localSLAt(text); got != want {
			t.Errorf("localSLAt(%q) = %d, want %d", text, got, want)
		}
	}
}

// On a held call, "Sl ctc kar lena" moves the stop to the entry at once;
// the LLM only audits it after.
func TestLocalMoveSLToCost(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.ctxs.outcome("42:1", TGEventEntered, tNow)
	r.res = LLMResult{Kind: "follow_up", Action: "move_sl", Confidence: "high", AboutCall: true}
	h.onMessage(context.Background(), reply(2, 1, "Sl ctc kar lena"), tNow)
	if len(s.follows) != 1 || s.follows[0].Action != model.TGMoveSL || s.follows[0].NewSLPaise != 19100 {
		t.Fatalf("follow-ups %+v", s.follows)
	}
}

// "Avoid" on a call not yet bought cancels it at once.
func TestLocalCancel(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	n := r.calls
	r.res = LLMResult{Kind: "follow_up", Action: "cancel", Confidence: "high", AboutCall: true}
	h.onMessage(context.Background(), reply(2, 1, "Avoid ❌"), tNow)
	if len(s.follows) != 1 || s.follows[0].Action != model.TGCancel || s.follows[0].CallKey() != "42:1" {
		t.Fatalf("follow-ups %+v", s.follows)
	}
	if r.calls != n+1 { // the audit read only
		t.Fatalf("LLM reads %d, want %d", r.calls, n+1)
	}
}

// A plain "Active" that replies to nothing is not local: the LLM decides
// whether it is about the call.
func TestLocalNeedsReply(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	n := r.calls
	r.res = LLMResult{Kind: "follow_up", Action: "activate", Confidence: "high", AboutCall: false}
	h.onMessage(context.Background(), inMsg{ChatID: 42, MsgID: 2, Text: "Active", Sent: tNow}, tNow.Add(time.Second))
	if r.calls != n+1 || len(activates(s)) != 0 {
		t.Fatalf("reads %d (want %d), activates %v", r.calls, n+1, activates(s))
	}
}
