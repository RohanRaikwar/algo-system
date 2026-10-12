package orderexec

import (
	"fmt"
	"log"
	"time"
)

// Margin pre-check for real entries. The broker's available margin is
// cached by RefreshMargin (called in the background); the order path only
// reads the cache, so no network call sits in front of an order. The
// broker still has the final word: an unknown or stale cache lets the BUY
// through.

// marginMaxAge is how old a cached margin may be and still refuse a BUY.
var marginMaxAge = 3 * time.Minute

type marginCache struct {
	paise int64
	at    time.Time
}

// RefreshMargin reads the available margin from the broker's RMS limits.
// No-op without a live session.
func (oe *OrderExecutor) RefreshMargin() error {
	if !oe.liveSession() || oe.cfg.MarginBufferPct < 0 {
		return nil
	}
	api := oe.brokerAPI()
	if api == nil {
		return errNoSession
	}
	res, err := api.RMSLimit()
	if err != nil {
		return fmt.Errorf("rms limits: %w", err)
	}
	if !statusOK(res) {
		return fmt.Errorf("rms limits status=false: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	v := data["net"]
	if s, _ := v.(string); s == "" {
		v = data["availablecash"]
	}
	p, err := rupeesToPaise(v)
	if err != nil {
		return fmt.Errorf("rms limits net %v: %w", v, err)
	}
	oe.mu.Lock()
	oe.margin = marginCache{paise: p, at: time.Now()}
	oe.mu.Unlock()
	return nil
}

// marginShort reports a real BUY the cached margin cannot pay for:
// price × qty plus MarginBufferPct. False when the cache is unknown or
// stale, or the check is off (MarginBufferPct < 0).
func (oe *OrderExecutor) marginShort(pricePaise, qty int64) (need, have int64, short bool) {
	if oe.cfg.MarginBufferPct < 0 || pricePaise <= 0 || qty <= 0 {
		return 0, 0, false
	}
	oe.mu.RLock()
	m := oe.margin
	oe.mu.RUnlock()
	need = pricePaise * qty * (100 + oe.cfg.MarginBufferPct) / 100
	if m.at.IsZero() || time.Since(m.at) > marginMaxAge {
		log.Printf("%s 💰 margin unknown or stale (age %s) — BUY left to the broker's check", oe.logPrefix(), time.Since(m.at).Round(time.Second))
		return need, m.paise, false
	}
	return need, m.paise, need > m.paise
}
