package orderexec

import (
	"testing"
	"time"
)

func TestCandidateExpiries_MondaySkipsToday(t *testing.T) {
	sp := NewStrikePicker(nil)

	// Monday March 30, 2026 10:00 IST — today is a shifted expiry day.
	// candidateExpiries must NOT include "30MAR26" (today).
	now := time.Date(2026, 3, 30, 10, 0, 0, 0, istZone)
	expiries := sp.candidateExpiries(now)

	for _, e := range expiries {
		if e == "30MAR26" {
			t.Fatalf("candidateExpiries should NOT include today's date (30MAR26), got: %v", expiries)
		}
	}
	if len(expiries) == 0 {
		t.Fatal("expected non-empty candidate list")
	}

	// First candidate should be 31MAR26 (next Tuesday)
	if expiries[0] != "31MAR26" {
		t.Errorf("expected first candidate to be 31MAR26, got %s (full list: %v)", expiries[0], expiries)
	}

	// Should include next-week fallback candidates (around April 7)
	hasNextWeek := false
	for _, e := range expiries {
		if e == "07APR26" {
			hasNextWeek = true
			break
		}
	}
	if !hasNextWeek {
		t.Errorf("expected next-week candidates (07APR26) in list: %v", expiries)
	}
}

func TestCandidateExpiries_TuesdaySkipsToday(t *testing.T) {
	sp := NewStrikePicker(nil)

	// Tuesday March 31, 2026 10:00 IST — normal expiry day.
	now := time.Date(2026, 3, 31, 10, 0, 0, 0, istZone)
	expiries := sp.candidateExpiries(now)

	for _, e := range expiries {
		if e == "31MAR26" {
			t.Fatalf("candidateExpiries should NOT include today (31MAR26) on Tuesday, got: %v", expiries)
		}
	}
	if len(expiries) == 0 {
		t.Fatal("expected non-empty candidate list")
	}

	// Should include next-week candidates (around April 7)
	hasNextWeek := false
	for _, e := range expiries {
		if e == "07APR26" {
			hasNextWeek = true
			break
		}
	}
	if !hasNextWeek {
		t.Errorf("expected next-week candidates (07APR26) in list: %v", expiries)
	}
}

func TestCandidateExpiries_WednesdayIncludesCurrentAndNextWeek(t *testing.T) {
	sp := NewStrikePicker(nil)

	// Wednesday April 1, 2026 10:00 IST — not an expiry day.
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, istZone)
	expiries := sp.candidateExpiries(now)

	// Should NOT include today (01APR26) or past dates
	for _, e := range expiries {
		if e == "01APR26" || e == "31MAR26" || e == "30MAR26" {
			t.Fatalf("candidateExpiries should NOT include today or past dates, got: %v", expiries)
		}
	}

	// Should include next Tuesday (07APR26) and its fallback week
	hasTues := false
	for _, e := range expiries {
		if e == "07APR26" {
			hasTues = true
			break
		}
	}
	if !hasTues {
		t.Errorf("expected 07APR26 in candidates: %v", expiries)
	}
}
