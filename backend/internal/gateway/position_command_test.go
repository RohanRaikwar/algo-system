package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

func TestPositionCommandHandler(t *testing.T) {
	var sent []string
	h := makePositionCommandHandler(func(_ context.Context, p string) error {
		sent = append(sent, p)
		return nil
	})
	do := func(method, body string) int {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, "/api/positions/command", strings.NewReader(body)))
		return rec.Code
	}

	if code := do(http.MethodPost, `{"strategy":"SENSEX_SR","side":"CALL","action":"set_levels","index_stop":8150000}`); code != http.StatusAccepted {
		t.Fatalf("good POST: %d", code)
	}
	var got model.PositionCommand
	if len(sent) != 1 || json.Unmarshal([]byte(sent[0]), &got) != nil || got.IndexStop != 8150000 || got.TS.IsZero() {
		t.Fatalf("published %v", sent)
	}

	for name, body := range map[string]string{
		"bad json":   `{`,
		"bad action": `{"strategy":"NIFTY50_SR","side":"CALL","action":"buy"}`,
		"no level":   `{"strategy":"NIFTY50_SR","side":"CALL","action":"set_levels"}`,
	} {
		if code := do(http.MethodPost, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := do(http.MethodGet, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", code)
	}
	if len(sent) != 1 {
		t.Errorf("bad requests published: %v", sent)
	}

	h = makePositionCommandHandler(func(context.Context, string) error { return errors.New("down") })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"strategy":"NIFTY50_SR","side":"PUT","action":"exit"}`)))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("publish failure: %d", rec.Code)
	}
}
