package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryLoginStartsAfterSuccess(t *testing.T) {
	calls, started := 0, 0
	login := func() error {
		calls++
		if calls < 3 {
			return errors.New("timeout")
		}
		return nil
	}
	ok := retryLogin(context.Background(), login, func() { started++ }, time.Millisecond, 2*time.Millisecond)
	if !ok || calls != 3 || started != 1 {
		t.Fatalf("ok=%v calls=%d started=%d, want true 3 1", ok, calls, started)
	}
}

func TestRetryLoginStopsOnContextEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	login := func() error {
		calls++
		if calls == 2 {
			cancel()
		}
		return errors.New("timeout")
	}
	started := false
	ok := retryLogin(ctx, login, func() { started = true }, time.Millisecond, time.Millisecond)
	if ok || started {
		t.Fatalf("ok=%v started=%v, want false false", ok, started)
	}
}
