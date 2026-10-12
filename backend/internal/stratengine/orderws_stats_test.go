package stratengine

import (
	"errors"
	"testing"
)

func TestOrderWSStats_DefaultsOff(t *testing.T) {
	var s orderWSStats
	snap := s.snapshot()
	if snap.Status != orderWSOff || snap.Since != "" || snap.LastPushAt != "" {
		t.Fatalf("zero snapshot = %+v", snap)
	}
}

func TestOrderWSStats_Lifecycle(t *testing.T) {
	var s orderWSStats
	s.setStatus(orderWSConnecting)
	s.onError(errors.New("dial refused"))
	if got := s.snapshot(); got.Status != orderWSDown || got.LastError != "dial refused" || got.Since == "" {
		t.Fatalf("after failed dial = %+v", got)
	}
	s.onState(true)
	s.onPush()
	s.onPush()
	s.onState(false)
	s.onError(errors.New("read timeout"))
	s.onState(true)
	got := s.snapshot()
	if got.Status != orderWSUp || got.Reconnects != 1 || got.Pushes != 2 || got.LastPushAt == "" || got.LastError != "read timeout" {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestOrderWSStats_ErrorWhileUpKeepsStatus(t *testing.T) {
	var s orderWSStats
	s.onState(true)
	s.onError(errors.New("x"))
	if got := s.snapshot().Status; got != orderWSUp {
		t.Fatalf("status = %s", got)
	}
}
