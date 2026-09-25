package config

import (
	"os"
	"testing"
)

// ── GetEnv ──

func TestGetEnv_Default(t *testing.T) {
	os.Unsetenv("TEST_KEY_1")
	if got := GetEnv("TEST_KEY_1", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestGetEnv_Set(t *testing.T) {
	os.Setenv("TEST_KEY_2", "hello")
	defer os.Unsetenv("TEST_KEY_2")
	if got := GetEnv("TEST_KEY_2", "fallback"); got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}

// ── GetEnvInt ──

func TestGetEnvInt_Default(t *testing.T) {
	os.Unsetenv("TEST_INT_1")
	if got := GetEnvInt("TEST_INT_1", 42); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestGetEnvInt_Valid(t *testing.T) {
	os.Setenv("TEST_INT_2", "99")
	defer os.Unsetenv("TEST_INT_2")
	if got := GetEnvInt("TEST_INT_2", 0); got != 99 {
		t.Fatalf("expected 99, got %d", got)
	}
}

func TestGetEnvInt_Invalid(t *testing.T) {
	os.Setenv("TEST_INT_3", "abc")
	defer os.Unsetenv("TEST_INT_3")
	if got := GetEnvInt("TEST_INT_3", 7); got != 7 {
		t.Fatalf("expected fallback 7, got %d", got)
	}
}

// ── GetEnvInt64 ──

func TestGetEnvInt64_Default(t *testing.T) {
	os.Unsetenv("TEST_I64_1")
	if got := GetEnvInt64("TEST_I64_1", 30000); got != 30000 {
		t.Fatalf("expected 30000, got %d", got)
	}
}

func TestGetEnvInt64_Valid(t *testing.T) {
	os.Setenv("TEST_I64_2", "60000")
	defer os.Unsetenv("TEST_I64_2")
	if got := GetEnvInt64("TEST_I64_2", 0); got != 60000 {
		t.Fatalf("expected 60000, got %d", got)
	}
}

// ── GetEnvBool ──

func TestGetEnvBool_Default_False(t *testing.T) {
	os.Unsetenv("TEST_BOOL_1")
	if got := GetEnvBool("TEST_BOOL_1", false); got {
		t.Fatal("expected false")
	}
}

func TestGetEnvBool_Default_True(t *testing.T) {
	os.Unsetenv("TEST_BOOL_2")
	if got := GetEnvBool("TEST_BOOL_2", true); !got {
		t.Fatal("expected true")
	}
}

func TestGetEnvBool_True(t *testing.T) {
	os.Setenv("TEST_BOOL_3", "TRUE")
	defer os.Unsetenv("TEST_BOOL_3")
	if got := GetEnvBool("TEST_BOOL_3", false); !got {
		t.Fatal("expected true for TRUE")
	}
}

func TestGetEnvBool_False(t *testing.T) {
	os.Setenv("TEST_BOOL_4", "no")
	defer os.Unsetenv("TEST_BOOL_4")
	if got := GetEnvBool("TEST_BOOL_4", true); got {
		t.Fatal("expected false for 'no'")
	}
}

// ── ParseTFString ──

func TestParseTFString_Valid(t *testing.T) {
	result := ParseTFString("60,300,900")
	if len(result) != 3 {
		t.Fatalf("expected 3 TFs, got %d", len(result))
	}
	if result[0] != 60 || result[1] != 300 || result[2] != 900 {
		t.Fatalf("unexpected values: %v", result)
	}
}

func TestParseTFString_WithInvalid(t *testing.T) {
	result := ParseTFString("60,abc,300, ,0,-1,900")
	if len(result) != 3 {
		t.Fatalf("expected 3 valid TFs, got %d: %v", len(result), result)
	}
}

func TestParseTFString_Empty(t *testing.T) {
	result := ParseTFString("")
	if len(result) != 0 {
		t.Fatalf("expected 0 TFs, got %d", len(result))
	}
}

// ── Validate ──

func TestValidate_AllSet(t *testing.T) {
	cfg := &Config{
		AngelAPIKey:     "key",
		AngelClientCode: "code",
		AngelPassword:   "pass",
		AngelTOTPSecret: "totp",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidate_Missing(t *testing.T) {
	cfg := &Config{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestValidate_PartialMissing(t *testing.T) {
	cfg := &Config{
		AngelAPIKey:   "key",
		AngelPassword: "pass",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for partial credentials")
	}
}
