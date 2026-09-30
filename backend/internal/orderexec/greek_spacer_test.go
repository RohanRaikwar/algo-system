package orderexec

import (
	"testing"
	"time"
)

func TestGreekSpacerWaitsBetweenCalls(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 20, 0, 0, time.UTC)
	var slept []time.Duration
	s := &greekSpacer{
		gap:   time.Second,
		now:   func() time.Time { return now },
		sleep: func(d time.Duration) { slept = append(slept, d); now = now.Add(d) },
	}

	s.wait() // first call: nothing to wait for
	now = now.Add(300 * time.Millisecond)
	s.wait() // second expiry right after the first
	now = now.Add(2 * time.Second)
	s.wait() // well past the gap

	if len(slept) != 1 || slept[0] != 700*time.Millisecond {
		t.Fatalf("slept %v, want [700ms]", slept)
	}
}
