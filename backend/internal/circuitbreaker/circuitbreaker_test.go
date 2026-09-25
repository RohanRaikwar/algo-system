package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_StartsClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed, got %v", cb.CurrentState())
	}
}

func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	errFail := errors.New("fail")

	for i := 0; i < 3; i++ {
		err := cb.Execute(func() error { return errFail })
		if err != errFail {
			t.Fatalf("expected errFail, got %v", err)
		}
	}

	if cb.CurrentState() != StateOpen {
		t.Errorf("expected Open after 3 failures, got %v", cb.CurrentState())
	}

	// Calls should be rejected immediately
	err := cb.Execute(func() error { return nil })
	if err != ErrCircuitOpen {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Trip the breaker
	errFail := errors.New("fail")
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return errFail })
	}
	if cb.CurrentState() != StateOpen {
		t.Fatal("expected Open")
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Next call should succeed and close the circuit
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after successful probe, got %v", cb.CurrentState())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	errFail := errors.New("fail")

	// Trip
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return errFail })
	}

	// Wait and fail the probe
	time.Sleep(60 * time.Millisecond)
	cb.Execute(func() error { return errFail })

	if cb.CurrentState() != StateOpen {
		t.Errorf("expected Open after failed probe, got %v", cb.CurrentState())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	errFail := errors.New("fail")

	// 2 failures, then a success
	cb.Execute(func() error { return errFail })
	cb.Execute(func() error { return errFail })
	cb.Execute(func() error { return nil }) // resets counter

	// 2 more failures shouldn't trip because counter was reset
	cb.Execute(func() error { return errFail })
	cb.Execute(func() error { return errFail })

	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed (counter should have reset), got %v", cb.CurrentState())
	}
}

func TestCircuitBreaker_OnStateChangeCallback(t *testing.T) {
	var transitions []State
	cb := NewCircuitBreaker(1, 50*time.Millisecond)
	cb.OnStateChange = func(from, to State) {
		transitions = append(transitions, to)
	}

	cb.Execute(func() error { return errors.New("fail") })

	if len(transitions) != 1 || transitions[0] != StateOpen {
		t.Errorf("expected [Open], got %v", transitions)
	}

	time.Sleep(60 * time.Millisecond)
	cb.Execute(func() error { return nil })

	if len(transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d: %v", len(transitions), transitions)
	}
	if transitions[1] != StateHalfOpen || transitions[2] != StateClosed {
		t.Errorf("expected [Open, HalfOpen, Closed], got %v", transitions)
	}
}

func TestCircuitBreaker_Failures(t *testing.T) {
	cb := NewCircuitBreaker(5, 100*time.Millisecond)
	errFail := errors.New("fail")

	if cb.Failures() != 0 {
		t.Errorf("expected 0 failures initially, got %d", cb.Failures())
	}

	cb.Execute(func() error { return errFail })
	cb.Execute(func() error { return errFail })

	if cb.Failures() != 2 {
		t.Errorf("expected 2 failures, got %d", cb.Failures())
	}

	cb.Execute(func() error { return nil })
	if cb.Failures() != 0 {
		t.Errorf("expected 0 failures after success, got %d", cb.Failures())
	}
}

func TestExecuteAlways_RunsWhenOpenAndSuccessCloses(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Hour)
	_ = cb.Execute(func() error { return errors.New("fail") })
	if cb.CurrentState() != StateOpen {
		t.Fatal("setup: breaker should be open")
	}
	ran := false
	if err := cb.ExecuteAlways(func() error { ran = true; return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ran {
		t.Fatal("ExecuteAlways must run fn even when open")
	}
	if cb.CurrentState() != StateClosed {
		t.Fatalf("success must close breaker, got %v", cb.CurrentState())
	}
}

func TestExecuteAlways_FailureRecorded(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Hour)
	_ = cb.ExecuteAlways(func() error { return errors.New("fail") })
	_ = cb.ExecuteAlways(func() error { return errors.New("fail") })
	if cb.CurrentState() != StateOpen {
		t.Fatalf("failures via ExecuteAlways must count, got %v", cb.CurrentState())
	}
}
