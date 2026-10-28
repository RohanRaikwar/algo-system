package tgsignal

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func reply(id, to int, text string) inMsg {
	m := msg(id, text)
	m.ReplyTo = to
	return m
}

func activates(s *sink) []string {
	var out []string
	for _, f := range s.follows {
		if f.Action == model.TGActivate {
			out = append(out, f.Key()+"→"+f.CallKey())
		}
	}
	return out
}

// TCI's real thread: "BUY ABOVE :-12, Current Price : 7", then the
// caller's price posts. The first post at the entry confirms the call,
// once; the later posts and the recap only add to the context.
func TestContextTCIConfirmsOnFirstPriceAtEntry(t *testing.T) {
	f, err := os.Open("testdata/tci.jsonl")
	if err != nil {
		t.Skip("no golden set:", err)
	}
	defer f.Close()
	h, s, j := newTestHandler(t, nil)
	h.slPct = 10
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var hm HistoryMsg
		if err := json.Unmarshal(sc.Bytes(), &hm); err != nil {
			t.Fatal(err)
		}
		if hm.MsgID < 62 || hm.MsgID > 72 {
			continue
		}
		h.onMessage(context.Background(), inMsg{ChatID: 42, MsgID: hm.MsgID, ReplyTo: hm.ReplyTo, Text: hm.Text, Sent: hm.Sent}, hm.Sent)
	}
	if len(s.calls) != 1 || s.calls[0].MsgID != 62 || s.calls[0].TriggerPaise != 1200 {
		t.Fatalf("calls %+v", s.calls)
	}
	if got := activates(s); len(got) != 1 || got[0] != "42:63→42:62" {
		t.Fatalf("activates %v, want one from the first price post 63", got)
	}
	if v, _ := verdictOf(t, j, 64); v != VerdictContext {
		t.Fatalf("later price post verdict %s", v)
	}
}

func TestContextPriceBelowEntryWaits(t *testing.T) {
	h, s, _ := newTestHandler(t, nil)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.onMessage(context.Background(), reply(2, 1, "185🔥🔥"), tNow)
	if len(s.follows) != 0 {
		t.Fatalf("below entry 191 confirmed: %+v", s.follows)
	}
	// A reply to the price post is in the same thread.
	h.onMessage(context.Background(), reply(3, 2, "Active 🔥🔥"), tNow)
	if got := activates(s); len(got) != 1 || got[0] != "42:3→42:1" {
		t.Fatalf("activates %v", got)
	}
	h.onMessage(context.Background(), reply(4, 1, "192🔥🔥"), tNow)
	if len(activates(s)) != 1 {
		t.Fatalf("confirmed twice: %v", activates(s))
	}
}

// "Not active yet, wait" goes to the LLM with the call in its context and,
// read as noise, does nothing.
func TestContextLLMGetsCallAndHistory(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.onMessage(context.Background(), reply(2, 1, "186🔥🔥"), tNow)
	r.res = LLMResult{Kind: "noise", Action: "none", Confidence: "high", AboutCall: true, Reason: "not active yet"}
	h.onMessage(context.Background(), reply(3, 1, "Abi active nhi hua h wait karna"), tNow)
	for _, want := range []string{ctxHeader, "BUY NIFTY 22750 PE above 191.00 SL 182.00", "waiting for the caller to confirm", "Caller price posts: 186.00", "replies to this call's thread"} {
		if !strings.Contains(r.reply, want) {
			t.Fatalf("context lacks %q:\n%s", want, r.reply)
		}
	}
	if len(s.follows) != 0 {
		t.Fatalf("follow-ups %+v", s.follows)
	}
	r.res = LLMResult{Kind: "follow_up", Action: "activate", Confidence: "high", AboutCall: true, Reason: "live"}
	h.onMessage(context.Background(), reply(4, 1, "Entry le lo abhi"), tNow)
	if !strings.Contains(r.reply, `"Abi active nhi hua h wait karna" → none`) {
		t.Fatalf("earlier message missing:\n%s", r.reply)
	}
	if got := activates(s); len(got) != 1 || got[0] != "42:4→42:1" {
		t.Fatalf("activates %v", got)
	}
}

// A message that replies to nothing links to the chat's only open call,
// and acts only when the LLM says it is about that call.
func TestContextUnrepliedNeedsAboutCall(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)

	r.res = LLMResult{Kind: "follow_up", Action: "exit", Confidence: "high", AboutCall: false}
	h.onMessage(context.Background(), msg(2, "exit kar do sab gold se"), tNow)
	if len(s.follows) != 0 || !strings.Contains(r.reply, "replies to nothing") {
		t.Fatalf("not-about-call message acted: %+v\n%s", s.follows, r.reply)
	}
	r.res.AboutCall = true
	h.onMessage(context.Background(), msg(3, "exit kar do"), tNow)
	if len(s.follows) != 1 || s.follows[0].CallKey() != "42:1" || s.follows[0].Action != model.TGExit {
		t.Fatalf("follow-ups %+v", s.follows)
	}
	// Chat with no trade words does not reach the LLM.
	n := r.calls
	h.onMessage(context.Background(), msg(4, "Ajao bhai log"), tNow)
	if r.calls != n {
		t.Fatalf("chat read by the LLM")
	}
}

func TestContextTwoOpenCallsNoLink(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, _, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	r.res = llmCall(22800, "PE", "191", "182")
	h.onMessage(context.Background(), msg(2, strings.Replace(realCall, "22750", "22800", 1)), tNow)
	if tc, _ := h.ctxs.link(msg(3, "exit"), tNow); tc != nil {
		t.Fatalf("linked to call %d with two open", tc.call.MsgID)
	}
	if tc, l := h.ctxs.link(reply(3, 2, "exit"), tNow); tc == nil || tc.call.MsgID != 2 || l != linkReply {
		t.Fatalf("reply not linked")
	}
}

func TestContextOutcomes(t *testing.T) {
	h, s, _ := newTestHandler(t, nil)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.ctxs.outcome("42:1", TGEventArmed, tNow)
	h.onMessage(context.Background(), reply(2, 1, "191🔥"), tNow)
	// stratengine refused the activate: the next post confirms again.
	h.ctxs.outcome("42:1", TGEventFollowUpIgnored, tNow)
	h.onMessage(context.Background(), reply(3, 1, "192🔥"), tNow)
	if len(activates(s)) != 2 {
		t.Fatalf("activates %v", activates(s))
	}
	h.ctxs.outcome("42:1", TGEventEntered, tNow)
	h.onMessage(context.Background(), reply(4, 1, "Active"), tNow)
	h.ctxs.outcome("42:1", TGEventExit, tNow)
	h.onMessage(context.Background(), reply(5, 1, "195🔥"), tNow)
	if len(activates(s)) != 2 {
		t.Fatalf("held or closed call confirmed: %v", activates(s))
	}
	// A closed context still links replies for an hour, then is gone.
	if tc, _ := h.ctxs.link(reply(6, 1, "x"), tNow.Add(30*time.Minute)); tc == nil {
		t.Fatal("closed context forgotten early")
	}
	if tc, _ := h.ctxs.link(reply(6, 1, "x"), tNow.Add(2*time.Hour)); tc != nil {
		t.Fatal("closed context kept")
	}
}

// After a restart, today's armed call comes back with its thread.
func TestContextLoadFromJournal(t *testing.T) {
	h, _, j := newTestHandler(t, nil)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.onMessage(context.Background(), reply(2, 1, "186🔥🔥"), tNow)
	h.onMessage(context.Background(), msg(5, strings.Replace(realCall, "22750", "22800", 1)), tNow)
	if err := j.AddOutcome(42, 1, "15:30:00 ARMED"); err != nil {
		t.Fatal(err)
	}
	if err := j.AddOutcome(42, 5, "15:30:00 ARMED | 15:31:00 DROPPED: expired"); err != nil {
		t.Fatal(err)
	}

	cs := newContexts()
	n, err := cs.load(j, []int64{42}, 10, tNow)
	if err != nil || n != 1 {
		t.Fatalf("load %d %v", n, err)
	}
	tc, l := cs.link(reply(3, 2, "190🔥"), tNow)
	if tc == nil || l != linkReply || tc.call.MsgID != 1 || tc.call.TriggerPaise != 19100 || len(tc.prices) != 1 {
		t.Fatalf("reloaded context %+v", tc)
	}
}

func TestPricePost(t *testing.T) {
	for text, want := range map[string]int64{"➡️280🔥🔥🔥": 28000, "104 🔥🔥🔥": 10400, "➡️➡️17🔥🔥": 1700, "12.5": 1250,
		"Active": 0, "280 To 350": 0, "📊280 𝗧𝗼 350": 0, "280 / 290": 0, "": 0} {
		if got := pricePost(text); got != want {
			t.Errorf("%q = %d, want %d", text, got, want)
		}
	}
}
