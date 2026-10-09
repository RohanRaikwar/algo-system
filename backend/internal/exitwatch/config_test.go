package exitwatch

import "testing"

// Auto-exit is opt-in: with EXITWATCH_AUTO_EXIT unset, EXIT is advice only.
func TestLoadConfigAutoExitOffByDefault(t *testing.T) {
	t.Setenv("EXITWATCH_AUTO_EXIT", "")
	if got := LoadConfig().AutoExit; len(got) != 0 {
		t.Fatalf("AutoExit = %v, want none", got)
	}
}
