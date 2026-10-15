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

func TestLoadConfig_Persist1sTokensAreTheIndexes(t *testing.T) {
	t.Setenv("STAGING_MODE", "true")
	t.Setenv("INDEX_TOKEN", "")
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	got := LoadConfig().Persist1sTokens
	if len(got) != 2 || !got["99926000"] || !got["99919000"] {
		t.Fatalf("Persist1sTokens = %v, want NIFTY and SENSEX", got)
	}
}

// Only the indexes build candles by default: no option data is stored.
func TestLoadConfig_CandleTokensAreTheIndexes(t *testing.T) {
	t.Setenv("STAGING_MODE", "true")
	t.Setenv("INDEX_TOKEN", "")
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	t.Setenv("CANDLE_TOKENS", "")
	got := LoadConfig().CandleTokens
	if len(got) != 2 || !got["99926000"] || !got["99919000"] {
		t.Fatalf("CandleTokens = %v, want only NIFTY and SENSEX", got)
	}
	t.Setenv("CANDLE_TOKENS", "99926000")
	if got := LoadConfig().CandleTokens; len(got) != 2 || !got["99919000"] {
		t.Fatalf("CANDLE_TOKENS override = %v, want SENSEX added", got)
	}
}
