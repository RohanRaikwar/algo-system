package mdengine

import "testing"

func TestConfigValidate_ProductionRequiresCredentials(t *testing.T) {
	cfg := Config{
		StagingMode:     false,
		AngelAPIKey:     "",
		AngelClientCode: "",
		EnabledTFs:      []int{60},
		RedisAddr:       "localhost:6379",
		SQLitePath:      "data/candles.db",
		RedisPassword:   "",
		MetricsAddr:     ":9090",
		AngelPassword:   "",
		AngelTOTPSecret: "",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for missing production credentials")
	}
}

func TestConfigValidate_RequiresAtLeastOneTF(t *testing.T) {
	cfg := Config{
		StagingMode: true,
		EnabledTFs:  nil,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for empty TF list")
	}
}

func TestConfigValidate_StagingAllowsMissingCredentials(t *testing.T) {
	cfg := Config{
		StagingMode: true,
		EnabledTFs:  []int{60, 120},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
