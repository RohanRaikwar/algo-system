package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRouter_HealthEndpoint(t *testing.T) {
	router := NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want %q", got, "application/json")
	}
	if rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("body = %q, want %q", rec.Body.String(), `{"status":"ok"}`)
	}
}
