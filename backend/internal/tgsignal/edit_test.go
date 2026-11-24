package tgsignal

import (
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

func edited(id int, text string) inMsg {
	m := msg(id, text)
	m.Edited, m.Posted = true, tNow
	return m
}

// The call edited into a non-call before we bought: cancelled.
func TestEditCallWithdrawnCancels(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	h.r.dispatch(edited(1, "❌ avoid this call"), tNow)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGCancel) || s.follows[0].CallKey() != "42:1" {
		t.Fatalf("follow-ups %v", got)
	}
}

// Withdrawn after our entry filled: no cancel (it would sell), noted.
func TestEditCallWithdrawnAfterFillKeeps(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, _ := newPipeline(t, r, true)
	var notes []string
	h.note = func(_, line string) { notes = append(notes, line) }
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	h.ctxs.outcome("42:1", TGEventFilled, tNow)
	h.r.dispatch(edited(1, "call galat thi, ignore"), tNow)
	h.r.waitIdle()
	if len(s.actions()) != 0 {
		t.Fatalf("follow-ups %v", s.actions())
	}
	if !strings.Contains(strings.Join(notes, "|"), "CALL_WITHDRAWN") {
		t.Fatalf("notes %v", notes)
	}
}

// A deleted waiting call is cancelled.
func TestDeletedCallCancels(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	h.r.deleted(42, []int{1}, tNow)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGCancel) {
		t.Fatalf("follow-ups %v", got)
	}
}

// "Active" edited into "not active yet" after it acted: no second action,
// the change is reported.
func TestEditedActiveReported(t *testing.T) {
	r := &scriptReader{res: func(text, ctxText string) (LLMResult, error) {
		if text == "Abi active nhi hua wait karo" {
			return LLMResult{Kind: "noise", Action: "none", Confidence: "high", AboutCall: true, Reason: "not active"}, nil
		}
		return agreeing(text, ctxText)
	}}
	h, s, _ := newPipeline(t, r, true)
	var notes []string
	h.note = func(_, line string) { notes = append(notes, line) }
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.waitIdle()
	h.r.dispatch(reply(2, 1, "Active"), tNow)
	h.r.waitIdle()
	e := reply(2, 1, "Abi active nhi hua wait karo")
	e.Edited, e.Posted = true, tNow
	h.r.dispatch(e, tNow)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGActivate) {
		t.Fatalf("follow-ups %v", got)
	}
	if !strings.Contains(strings.Join(notes, "|"), "FOLLOW_UP_EDITED: was activate") {
		t.Fatalf("notes %v", notes)
	}
}
