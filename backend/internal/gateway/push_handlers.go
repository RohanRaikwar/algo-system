package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"trading-systemv1/internal/push"
)

const maxSubscriptionBody = 4 << 10

// RegisterPushRoutes adds the Web Push subscription endpoints. With VAPID keys
// unset every route except OPTIONS answers 503.
func RegisterPushRoutes(mux *http.ServeMux, sender *push.Sender) {
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	// guard handles CORS, preflight, method and the disabled case. It
	// returns false when the request has been answered.
	guard := func(w http.ResponseWriter, r *http.Request, method string) bool {
		SetCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return false
		}
		if r.Method != method {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return false
		}
		if !sender.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": push.ErrDisabled.Error()})
			return false
		}
		return true
	}

	mux.HandleFunc("/api/push/vapid-public-key", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": sender.PublicKey()})
	})

	mux.HandleFunc("/api/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r, http.MethodPost) {
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSubscriptionBody))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		sub, err := push.ParseSubscription(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := sender.Subscribe(r.Context(), sub); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("/api/push/unsubscribe", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxSubscriptionBody)).Decode(&req); err != nil || req.Endpoint == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint required"})
			return
		}
		if err := sender.Unsubscribe(r.Context(), req.Endpoint); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("/api/push/test", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r, http.MethodPost) {
			return
		}
		// Optional {"endpoint": ...}: test only that device. Empty body: all.
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		json.NewDecoder(io.LimitReader(r.Body, maxSubscriptionBody)).Decode(&req)
		n := push.Notification{
			Title: "Test notification",
			Body:  "Push notifications are working.",
			Tag:   "test",
			URL:   "/",
		}
		var (
			sent int
			err  error
		)
		if req.Endpoint != "" {
			sent, err = sender.SendTo(r.Context(), req.Endpoint, n)
		} else {
			sent, err = sender.Send(r.Context(), n)
		}
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, push.ErrDisabled):
				status = http.StatusServiceUnavailable
			case errors.Is(err, push.ErrNotSubscribed):
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"sent": sent})
	})
}
