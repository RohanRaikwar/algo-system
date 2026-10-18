package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"trading-systemv1/internal/model"
)

// makePositionCommandHandler handles POST /api/positions/command: a
// dashboard edit of an open position (move stop/target, exit). It only
// checks the shape and forwards it; stratengine decides, and refuses
// real-order strategies.
func makePositionCommandHandler(publish func(ctx context.Context, payload string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusOK)
			return
		case http.MethodPost:
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var cmd model.PositionCommand
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cmd); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := cmd.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cmd.TS = time.Now()
		b, _ := json.Marshal(cmd)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := publish(ctx, string(b)); err != nil {
			http.Error(w, "publish failed", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}
}
