package gateway

import "testing"

func TestSignalLogJournalPathsAddExitwatchOnlyToLog(t *testing.T) {
	t.Setenv("STRAT_JOURNAL_PATH", "data/signals.db")
	t.Setenv("STRAT_IND_JOURNAL_PATH", "")
	t.Setenv("EXITWATCH_JOURNAL_PATH", "")

	analytics := resolveSignalJournalPaths()
	for _, p := range analytics {
		if p == "data/exitwatch_signals.db" {
			t.Fatal("daily analytics must not read the exitwatch journal")
		}
	}
	log := resolveSignalLogJournalPaths()
	if len(log) != len(analytics)+1 || log[len(log)-1] != "data/exitwatch_signals.db" {
		t.Fatalf("log paths: %v", log)
	}
	t.Setenv("EXITWATCH_JOURNAL_PATH", "/tmp/x.db")
	if got := resolveSignalLogJournalPaths(); got[len(got)-1] != "/tmp/x.db" {
		t.Fatalf("env override ignored: %v", got)
	}
}
