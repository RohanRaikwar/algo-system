package stratengine

import (
	"log"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/store/sqlite"
)

// rangeWarmer is a strategy whose range context is rebuilt from history.
type rangeWarmer interface {
	Name() string
	Warmup(candles []model.TFCandle) int
}

// warmupRangeStrategies replays recent 1m NIFTY candles into the range
// strategies so 15m ADX and support/resistance are ready at the open.
// Strategies dedupe on candle time, so candles a restored snapshot
// already holds are skipped. A missing store means a cold start.
func (svc *Service) warmupRangeStrategies() {
	var warmers []rangeWarmer
	if svc.cfg.RangeEnabled && svc.nifty50RangeStrategy != nil {
		warmers = append(warmers, svc.nifty50RangeStrategy)
	}
	if svc.cfg.RangeICEnabled && svc.nifty50RangeICStrategy != nil {
		warmers = append(warmers, svc.nifty50RangeICStrategy)
	}
	if svc.cfg.SREnabled && svc.srStrategy != nil {
		warmers = append(warmers, svc.srStrategy)
	}
	if len(warmers) == 0 {
		return
	}
	reader, err := sqlite.NewReader(svc.cfg.SQLitePath)
	if err != nil {
		log.Printf("[stratengine] range warmup skipped (%s): %v — cold start", svc.cfg.SQLitePath, err)
		return
	}
	defer reader.Close()
	warmRange(reader, warmers, svc.cfg.WarmupDays, time.Now())
}

func warmRange(reader model.CandleReader, warmers []rangeWarmer, days int, now time.Time) {
	if days <= 0 {
		days = 5
	}
	after := now.AddDate(0, 0, -days).Unix()
	candles, err := reader.ReadTFCandles(config.IndexExchange(), config.IndexToken(), 60, after)
	if err != nil {
		log.Printf("[stratengine] range warmup read error: %v — cold start", err)
		return
	}
	for _, w := range warmers {
		n := w.Warmup(candles)
		log.Printf("[stratengine] 🔥 %s warmed with %d/%d 1m candles (%d days)", w.Name(), n, len(candles), days)
	}
}
