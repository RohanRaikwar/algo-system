package indicator

import (
	"log"

	"trading-systemv1/internal/model"
)

// SQLiteReader is the interface needed for backfill reads.
type SQLiteReader interface {
	// ReadRecentTFCandles returns each token's latest perToken candles of
	// the timeframe, oldest first.
	ReadRecentTFCandles(tf, perToken int) ([]model.TFCandle, error)
}

// Restorer orchestrates indicator engine state restoration on MS2 startup.
// It follows a priority chain: Redis snapshot → SQLite snapshot → cold start.
type Restorer struct {
	configs []TFIndicatorConfig
}

// NewRestorer creates a new Restorer for the given indicator configs.
func NewRestorer(configs []TFIndicatorConfig) *Restorer {
	return &Restorer{configs: configs}
}

// RestoreFromSnapshot attempts to restore an engine from a snapshot.
// If snapshot is nil, returns a fresh engine (cold start).
func (r *Restorer) RestoreFromSnap(snap *EngineSnapshot) (*Engine, error) {
	if snap == nil {
		log.Println("[restorer] no snapshot found — cold starting indicator engine")
		return NewEngine(r.configs), nil
	}

	log.Printf("[restorer] restoring from snapshot (version=%d, streamID=%s, tokens=%d)",
		snap.Version, snap.StreamID, len(snap.Tokens))

	engine, err := RestoreEngine(r.configs, snap)
	if err != nil {
		log.Printf("[restorer] WARNING: snapshot restore failed: %v — falling back to cold start", err)
		return NewEngine(r.configs), nil
	}

	log.Printf("[restorer] ✅ restored indicator engine from snapshot")
	return engine, nil
}

// ReplayCandles feeds a slice of TF candles into the engine to catch up
// from the snapshot to current state. Returns the number of candles replayed.
func (r *Restorer) ReplayCandles(engine *Engine, candles []model.TFCandle) int {
	count := 0
	for _, tfc := range candles {
		if tfc.Forming {
			continue
		}
		engine.Process(tfc)
		count++
	}
	log.Printf("[restorer] replayed %d TF candles to catch up", count)
	return count
}

// BackfillFromSQLite reads historical TF candles from SQLite and feeds them
// into the engine to warm up cold indicators. This should be called after
// engine creation/restore and before starting the live stream consumer.
// It is safe after a snapshot restore: candles the restored state already
// covers are applied only to indicators still warming (see Engine.Process).
//
// maxPeriod is the largest indicator period (e.g. 200 for SMA_200).
// It reads the last `maxPeriod` candles of each token per TF.
// If onResults is non-nil, it is called with the indicator results for each candle,
// allowing the caller to write them to Redis for history population.
func (r *Restorer) BackfillFromSQLite(engine *Engine, reader SQLiteReader, onResults func([]model.IndicatorResult)) int {
	if reader == nil {
		return 0
	}

	// Find max period across all configs
	maxPeriod := 0
	for _, cfg := range r.configs {
		for _, ind := range cfg.Indicators {
			if ind.Period > maxPeriod {
				maxPeriod = ind.Period
			}
		}
	}
	if maxPeriod == 0 {
		return 0
	}

	total := 0
	for _, cfg := range r.configs {
		candles, err := reader.ReadRecentTFCandles(cfg.TF, maxPeriod)
		if err != nil {
			log.Printf("[restorer] WARNING: failed to read TF=%d candles from SQLite: %v", cfg.TF, err)
			continue
		}

		fed := 0
		for _, tfc := range candles {
			tfc.Forming = false
			results := engine.Process(tfc)
			if onResults != nil && len(results) > 0 {
				onResults(results)
			}
			fed++
		}
		total += fed
		if fed > 0 {
			log.Printf("[restorer] backfilled %d candles from SQLite for TF=%d", fed, cfg.TF)
		}
	}

	if total > 0 {
		log.Printf("[restorer] ✅ backfilled %d total candles from SQLite", total)
	}
	return total
}
