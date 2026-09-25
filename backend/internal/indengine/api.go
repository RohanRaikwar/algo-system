package indengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// startHTTP launches the HTTP server for /reload and /healthz endpoints.
func (svc *Service) startHTTP(ctx context.Context) {
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/reload", svc.handleReload)
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "ok")
		})
		log.Printf("[indengine] HTTP server on %s (/reload, /healthz)", svc.cfg.HTTPAddr)
		if err := http.ListenAndServe(svc.cfg.HTTPAddr, mux); err != nil {
			log.Printf("[indengine] HTTP server error: %v", err)
		}
	}()
}

// handleReload handles POST /reload for live config updates via HTTP.
func (svc *Service) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var newConfigs []indicator.TFIndicatorConfig
	if err := json.NewDecoder(r.Body).Decode(&newConfigs); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := indicator.ValidateConfigs(newConfigs); err != nil {
		http.Error(w, "validation: "+err.Error(), http.StatusBadRequest)
		return
	}
	var preserved, created int
	if err := svc.onEngine(r.Context(), func() { preserved, created = svc.reloadLocked(context.Background(), newConfigs) }); err != nil {
		http.Error(w, "engine unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"preserved": preserved,
		"created":   created,
	})
}

// startConfigSubscriber listens on Redis PubSub for dynamic indicator config updates.
func (svc *Service) startConfigSubscriber(ctx context.Context) {
	go func() {
		pubsub := svc.redisReader.SubscribeChannel(ctx, "config:indicators")
		if pubsub == nil {
			log.Println("[indengine] WARNING: could not subscribe to config:indicators")
			return
		}
		defer pubsub.Close()
		log.Println("[indengine] subscribed to config:indicators for dynamic reload")

		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				log.Printf("[indengine] received config update: %s", msg.Payload)
				svc.reloadFromSpecs(ctx, ParseIndicatorSpecs(msg.Payload))
			}
		}
	}()
}

// reloadFromSpecs rebuilds TF configs from indicator specs and reloads the
// engine on its owner goroutine.
func (svc *Service) reloadFromSpecs(ctx context.Context, newSpecs []indicator.IndicatorConfig) {
	newConfigs := make([]indicator.TFIndicatorConfig, len(svc.cfg.EnabledTFs))
	for i, tf := range svc.cfg.EnabledTFs {
		newConfigs[i] = indicator.TFIndicatorConfig{TF: tf, Indicators: newSpecs}
	}
	if err := indicator.ValidateConfigs(newConfigs); err != nil {
		log.Printf("[indengine] invalid config: %v", err)
		return
	}
	if err := svc.onEngine(ctx, func() { svc.reloadLocked(ctx, newConfigs) }); err != nil {
		log.Printf("[indengine] reload not applied: %v", err)
	}
}

// reloadLocked swaps in new configs and, if indicators were added, warms
// them from the Redis candle streams. Indicators that were kept skip every
// replayed candle they have already applied, so only the new ones take the
// history. Runs on the owner, so live candles wait (queued) until the
// backfill is done and then arrive in order. Owner only.
func (svc *Service) reloadLocked(ctx context.Context, newConfigs []indicator.TFIndicatorConfig) (preserved, created int) {
	preserved, created = svc.engine.ReloadConfigs(newConfigs)
	log.Printf("[indengine] reloaded: preserved=%d, created=%d", preserved, created)
	if created == 0 {
		return preserved, created
	}

	backfillCh := make(chan model.TFCandle, 5000)
	go func() {
		for _, stream := range svc.streams {
			_, err := svc.redisReader.ReplayFromID(ctx, stream, "0", backfillCh)
			if err != nil {
				log.Printf("[indengine] reload backfill error on %s: %v", stream, err)
			}
		}
		close(backfillCh)
	}()

	backfillCount := 0
	for tfc := range backfillCh {
		if !tfc.Forming {
			svc.apply(ctx, tfc)
			backfillCount++
		}
	}
	log.Printf("[indengine] ✅ reload backfill: replayed %d candles for new indicators", backfillCount)
	return preserved, created
}
