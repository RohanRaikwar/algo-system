package optionpicker

import (
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	out  []Contract
	err  error
	hits int
}

func (f *fakeSource) Load(time.Time) ([]Contract, error) { f.hits++; return f.out, f.err }

func TestChainCacheKeepsLastGoodAndBacksOff(t *testing.T) {
	src := &fakeSource{out: []Contract{{Strike: 22700, Option: "CE"}}}
	c := NewChainCache(src, 15*time.Second, 60*time.Second)
	t0 := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	if wait := c.Refresh(t0); wait != 15*time.Second {
		t.Fatalf("wait %v", wait)
	}
	src.err = errors.New("Angel One API rate limit: Access denied because of exceeding access rate")
	if wait := c.Refresh(t0.Add(15 * time.Second)); wait != 60*time.Second {
		t.Fatalf("rate limit wait %v, want 60s backoff", wait)
	}
	got, at := c.Snapshot()
	if len(got) != 1 || !at.Equal(t0) || c.LastError() == nil {
		t.Fatalf("snapshot %v at %v err %v", got, at, c.LastError())
	}
	src.err = errors.New("timeout")
	if wait := c.Refresh(t0.Add(75 * time.Second)); wait != 15*time.Second {
		t.Fatalf("plain error wait %v", wait)
	}
}
