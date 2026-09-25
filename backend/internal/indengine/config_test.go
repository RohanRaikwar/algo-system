package indengine

import (
	"testing"

	"trading-systemv1/internal/indicator"
)

func TestConfigValidate_OK(t *testing.T) {
	cfg := Config{
		EnabledTFs:        []int{60},
		SnapshotIntervalS: 30,
		PELIntervalS:      30,
		ConsumerGroup:     "indengine",
		ConsumerName:      "worker-1",
		IndicatorConfigs: []indicator.TFIndicatorConfig{
			{TF: 60, Indicators: []indicator.IndicatorConfig{{Type: "EMA", Period: 9}}},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() returned unexpected error: %v", err)
	}
}

func TestConfigValidate_Errors(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "missing tfs",
			cfg: Config{
				SnapshotIntervalS: 30,
				PELIntervalS:      30,
				ConsumerGroup:     "g",
				ConsumerName:      "n",
			},
		},
		{
			name: "invalid snapshot interval",
			cfg: Config{
				EnabledTFs:        []int{60},
				SnapshotIntervalS: 0,
				PELIntervalS:      30,
				ConsumerGroup:     "g",
				ConsumerName:      "n",
			},
		},
		{
			name: "invalid pel interval",
			cfg: Config{
				EnabledTFs:        []int{60},
				SnapshotIntervalS: 30,
				PELIntervalS:      0,
				ConsumerGroup:     "g",
				ConsumerName:      "n",
			},
		},
		{
			name: "empty consumer group",
			cfg: Config{
				EnabledTFs:        []int{60},
				SnapshotIntervalS: 30,
				PELIntervalS:      30,
				ConsumerGroup:     "",
				ConsumerName:      "n",
			},
		},
		{
			name: "empty consumer name",
			cfg: Config{
				EnabledTFs:        []int{60},
				SnapshotIntervalS: 30,
				PELIntervalS:      30,
				ConsumerGroup:     "g",
				ConsumerName:      "",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}
