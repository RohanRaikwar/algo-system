package stratengine

import "testing"

func validBaseConfig() Config {
	return Config{
		SubscribeTokenKeys: []string{"NSE:99926000"},
		LiveOrders:         false,
		DynamicStrikes:     false,
		OptionAutomation:   false,
		CallFNOToken:       "111",
		PutFNOToken:        "222",
	}
}

func TestConfigValidate_OK(t *testing.T) {
	cfg := validBaseConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestConfigValidate_RequiresSubscribeTokens(t *testing.T) {
	cfg := validBaseConfig()
	cfg.SubscribeTokenKeys = nil
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when STRAT_SUBSCRIBE_TOKENS is empty")
	}
}

func TestConfigValidate_LiveOrdersRequireCredentials(t *testing.T) {
	cfg := validBaseConfig()
	cfg.LiveOrders = true
	cfg.AngelAPIKey = ""
	cfg.AngelClientID = ""
	cfg.AngelPassword = ""
	cfg.AngelTOTP = ""
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected credential validation error for live orders")
	}
}

func TestConfigValidate_DynamicStrikesAutoDisabledWithoutLiveOrders(t *testing.T) {
	// Dynamic strikes should work without LiveOrders if credentials are present
	cfg := validBaseConfig()
	cfg.DynamicStrikes = true
	cfg.LiveOrders = false
	cfg.AngelAPIKey = "key"
	cfg.AngelClientID = "client"
	cfg.AngelPassword = "pass"
	cfg.AngelTOTP = "totp"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v — dynamic strikes should work in paper mode with credentials", err)
	}

	// But should fail without credentials
	cfg2 := validBaseConfig()
	cfg2.DynamicStrikes = true
	cfg2.LiveOrders = false
	if err := cfg2.Validate(); err == nil {
		t.Fatalf("expected error when dynamic strikes enabled without credentials")
	}
}

func TestConfigValidate_OptionAutomationAutoDisabledWithoutDynamicStrikes(t *testing.T) {
	cfg := validBaseConfig()
	cfg.OptionAutomation = true
	cfg.DynamicStrikes = false
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when option automation enabled without dynamic strikes")
	}
}

func TestConfigValidate_StaticTokensRequiredWithoutDynamicStrikes(t *testing.T) {
	cfg := validBaseConfig()
	cfg.CallFNOToken = ""
	cfg.PutFNOToken = ""
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error: no dynamic strikes and no static FNO tokens")
	}

	cfg.DynamicStrikes = true
	cfg.AngelAPIKey = "key"
	cfg.AngelClientID = "client"
	cfg.AngelPassword = "pass"
	cfg.AngelTOTP = "totp"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("dynamic strikes with no static tokens should be valid: %v", err)
	}
}

func TestLoadConfig_FNOTokensDefaultEmpty(t *testing.T) {
	t.Setenv("STRAT_CALL_FNO_TOKEN", "")
	t.Setenv("STRAT_PUT_FNO_TOKEN", "")
	cfg := LoadConfig()
	if cfg.CallFNOToken != "" || cfg.PutFNOToken != "" {
		t.Fatalf("FNO tokens default to %q/%q, want empty", cfg.CallFNOToken, cfg.PutFNOToken)
	}
}

func TestLoadConfig_MetricsAddrDefault(t *testing.T) {
	t.Setenv("STRAT_METRICS_ADDR", "")
	if got := LoadConfig().MetricsAddr; got != ":9096" {
		t.Fatalf("MetricsAddr default %q, want :9096", got)
	}
}
