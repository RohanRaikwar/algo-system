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
