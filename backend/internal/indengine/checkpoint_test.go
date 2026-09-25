package indengine

import (
	"testing"

	"trading-systemv1/internal/indicator"
)

func TestHasReplayCheckpoint(t *testing.T) {
	tests := []struct {
		name string
		snap *indicator.EngineSnapshot
		want bool
	}{
		{name: "nil snapshot", snap: nil, want: false},
		{
			name: "v1 with valid stream_id",
			snap: &indicator.EngineSnapshot{Version: 1, StreamID: "123-0"},
			want: true,
		},
		{
			name: "v1 with invalid stream_id",
			snap: &indicator.EngineSnapshot{Version: 1, StreamID: "shutdown"},
			want: false,
		},
		{
			name: "v2 ignores legacy stream_id",
			snap: &indicator.EngineSnapshot{Version: 2, StreamID: "123-0"},
			want: false,
		},
		{
			name: "v2 uses stream_ids map",
			snap: &indicator.EngineSnapshot{
				Version:   2,
				StreamIDs: map[string]string{"candle:60s:NSE:99926000": "456-0"},
			},
			want: true,
		},
		{
			name: "v2 with invalid mapped ids",
			snap: &indicator.EngineSnapshot{
				Version:   2,
				StreamIDs: map[string]string{"candle:60s:NSE:99926000": "bad"},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasReplayCheckpoint(tc.snap)
			if got != tc.want {
				t.Fatalf("hasReplayCheckpoint() = %v, want %v", got, tc.want)
			}
		})
	}
}
