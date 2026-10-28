package tgsignal

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "tg.session")
	body := `{"Version":1,"Data":{}}`
	c := Config{SessionPath: path, SessionB64: base64.StdEncoding.EncodeToString([]byte(body))}

	if ok, err := c.SeedSession(); !ok || err != nil {
		t.Fatalf("seed: %v %v", ok, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != body {
		t.Fatalf("file %q", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	// The volume's file wins on later starts.
	_ = os.WriteFile(path, []byte(`{"newer":true}`), 0o600)
	if ok, _ := c.SeedSession(); ok {
		t.Fatal("overwrote an existing session")
	}
	if ok, err := (Config{SessionPath: filepath.Join(dir, "x"), SessionB64: base64.StdEncoding.EncodeToString([]byte("junk"))}).SeedSession(); ok || err == nil {
		t.Fatal("accepted a non-session value")
	}
}

func TestLoadLLMExtra(t *testing.T) {
	t.Setenv("TG_LLM_2_API_KEY", "k2")
	t.Setenv("TG_LLM_3_API_KEY", "k3")
	t.Setenv("TG_LLM_3_BASE_URL", "https://api.cerebras.ai/v1")
	t.Setenv("TG_LLM_3_MODEL", "llama-4")
	t.Setenv("TG_LLM_3_TPM", "60000")
	t.Setenv("TG_LLM_4_MODEL", "only a model: skipped")
	c := Config{LLMBaseURL: "https://api.groq.com/openai/v1", LLMModel: "qwen", LLMTPM: 8000, LLMRPM: 30}
	c.LLMExtra = loadLLMExtra()
	if len(c.LLMExtra) != 2 {
		t.Fatalf("extra %+v", c.LLMExtra)
	}
	k2, k3 := c.LLMExtra[0].withDefaults(c), c.LLMExtra[1].withDefaults(c)
	if k2.BaseURL != c.LLMBaseURL || k2.Model != "qwen" || k2.TPM != 8000 || k2.APIKey != "k2" {
		t.Fatalf("TG_LLM_2 %+v", k2)
	}
	if k3.BaseURL != "https://api.cerebras.ai/v1" || k3.Model != "llama-4" || k3.TPM != 60000 || k3.RPM != 30 {
		t.Fatalf("TG_LLM_3 %+v", k3)
	}
}
