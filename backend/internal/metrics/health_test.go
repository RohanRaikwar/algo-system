package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func healthz(t *testing.T, h *HealthStatus) (int, map[string]interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return rec.Code, body
}

// A connected socket that has stopped delivering ticks must not report healthy.
func TestHealthz_StaleFeedIsDegraded(t *testing.T) {
	h := NewHealthStatus()
	h.SetWSConnected(true)
	h.SetRedisConnected(true)
	h.SetSQLiteOK(true)
	h.SetLastTickTime(time.Now().Add(-time.Minute))

	if code, _ := healthz(t, h); code != http.StatusOK {
		t.Fatalf("fresh status = %d, want 200", code)
	}

	h.SetFeedStale(true)
	code, body := healthz(t, h)
	if code != http.StatusServiceUnavailable || body["status"] != "degraded" || body["feed_stale"] != true {
		t.Fatalf("stale feed: code=%d body=%v, want 503 degraded feed_stale=true", code, body)
	}
}
