package gateway

import (
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
)

func TestBuildChannels_ExcludesIndicatorExplicitChannels(t *testing.T) {
	h := &Hub{
		TFs:        []int{60, 300},
		Tokens:     []string{"NSE:99926000"},
		Indicators: []string{"EMA_9", "SMA_20"},
	}

	got := h.buildChannels()
	want := []string{
		"pub:candle:60s:NSE:99926000",
		"pub:candle:300s:NSE:99926000",
		"pub:candle:1s:NSE:99926000",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildChannels mismatch:\n got:  %v\n want: %v", got, want)
	}
}

func TestSetCORS_AllowAny(t *testing.T) {
	prev := allowedOrigins
	allowedOrigins = []string{"*"}
	t.Cleanup(func() { allowedOrigins = prev })

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()

	SetCORS(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow-origin mismatch: got %q want %q", got, "*")
	}
}

func TestSetCORS_AllowListedOrigin(t *testing.T) {
	prev := allowedOrigins
	allowedOrigins = []string{"https://app.example.com", "https://admin.example.com"}
	t.Cleanup(func() { allowedOrigins = prev })

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	rec := httptest.NewRecorder()

	SetCORS(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
		t.Fatalf("allow-origin mismatch: got %q want %q", got, "https://admin.example.com")
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("vary header mismatch: got %q want %q", got, "Origin")
	}
}

func TestSetCORS_DisallowUnlistedOrigin(t *testing.T) {
	prev := allowedOrigins
	allowedOrigins = []string{"https://app.example.com", "https://admin.example.com"}
	t.Cleanup(func() { allowedOrigins = prev })

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	SetCORS(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin mismatch: got %q want empty", got)
	}
}

func TestRunPattern_IncludesSignalAndPnlChannels(t *testing.T) {
	if !slices.Contains(dynamicPubSubPatterns, "pub:signal") {
		t.Fatalf("dynamic patterns missing pub:signal")
	}
	if !slices.Contains(dynamicPubSubPatterns, "pub:pnl") {
		t.Fatalf("dynamic patterns missing pub:pnl")
	}
}
