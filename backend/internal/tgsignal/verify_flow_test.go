package tgsignal

import (
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

// The LLM reads the call; the caller's later "Active" is read by code and
// sent at once, with no LLM read of its own.
func TestFlowVerifiedCallActiveByCode(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	n := r.n()
	h.r.dispatch(reply(2, 1, "Active 🔥🔥"), tNow)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGActivate) {
		t.Fatalf("follow-ups %v", got)
	}
	if r.n() != n {
		t.Fatalf("Active read by the LLM (%d reads, want %d)", r.n(), n)
	}
}

// A follow-up the code cannot read goes to the LLM with the call's trade
// context; its activate is sent.
func TestFlowFollowUpReadInContext(t *testing.T) {
	var ctx string
	r := &scriptReader{res: func(text, ctxText string) (LLMResult, error) {
		if text == "Entry le lo abhi, market strong hai" {
			ctx = ctxText
			return LLMResult{Kind: "follow_up", Action: "activate", Confidence: "high", AboutCall: true, Reason: "live"}, nil
		}
		return agreeing(text, ctxText)
	}}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	h.r.dispatch(reply(2, 1, "Entry le lo abhi, market strong hai"), tNow)
	h.r.waitIdle()
	if !strings.HasPrefix(ctx, ctxHeader) {
		t.Fatalf("follow-up read without the trade context: %q", ctx)
	}
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGActivate) {
		t.Fatalf("follow-ups %v", got)
	}
}

// An LLM-read activation that beats the call check is held, then sent
// once the check agrees.
func TestFlowLLMActivationHeldUntilVerified(t *testing.T) {
	gate := make(chan struct{})
	r := &scriptReader{res: func(text, ctxText string) (LLMResult, error) {
		if text == realCall {
			<-gate
			return agreeing(text, ctxText)
		}
		return LLMResult{Kind: "follow_up", Action: "activate", Confidence: "high", AboutCall: true, Reason: "live"}, nil
	}}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	waitFor(t, "arm", func() bool { c, _ := s.counts(); return c == 1 })
	h.r.dispatch(reply(2, 1, "Entry le lo abhi, market strong hai"), tNow)
	waitFor(t, "activation read", func() bool { return r.n() == 2 })
	if _, f := s.counts(); f != 0 {
		t.Fatal("activation sent before the call check")
	}
	close(gate)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGActivate) || s.follows[0].MsgID != 2 {
		t.Fatalf("follow-ups %v", got)
	} // The held activation keeps its arrival time, not its release time.
	if f := s.follows[0]; !f.ReceivedAt.Equal(tNow) || !f.SentAt.Equal(tNow) {
		t.Fatalf("stamps received=%v sent=%v, want %v", f.ReceivedAt, f.SentAt, tNow)
	}
}

// A reply in a call's trade context is routed without reading the
// journal (no SQLite read on the hot path).
func TestFlowContextReplyNoJournalRead(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, j := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	before := j.textReads.Load()
	h.r.dispatch(reply(2, 1, "Active"), tNow)
	h.r.waitIdle()
	if n := j.textReads.Load() - before; n != 0 {
		t.Fatalf("%d journal reads for a context reply", n)
	}
	if len(s.actions()) != 1 {
		t.Fatalf("follow-ups %v", s.actions())
	}
}
