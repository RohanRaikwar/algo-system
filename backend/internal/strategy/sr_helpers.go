package strategy

import (
	"time"

	"trading-systemv1/internal/model"
)

// minuteVolume turns cumulative day-volume ticks into per-minute volume.
type minuteVolume struct {
	minute      time.Time
	startVol    int64 // day volume when the minute began
	lastDayVol  int64
	initialized bool
}

// observe feeds one tick; it returns a finished minute and its volume.
func (m *minuteVolume) observe(ts time.Time, dayVol int64) (time.Time, int64, bool) {
	if dayVol <= 0 {
		return time.Time{}, 0, false
	}
	minute := ts.Truncate(time.Minute)
	if !m.initialized {
		m.minute, m.startVol, m.lastDayVol, m.initialized = minute, dayVol, dayVol, true
		return time.Time{}, 0, false
	}
	if dayVol < m.lastDayVol { // new session: counter reset
		m.minute, m.startVol, m.lastDayVol = minute, 0, dayVol
		return time.Time{}, 0, false
	}
	var done time.Time
	var vol int64
	var ok bool
	if minute.After(m.minute) {
		done, vol, ok = m.minute, m.lastDayVol-m.startVol, true
		m.minute, m.startVol = minute, m.lastDayVol
	}
	m.lastDayVol = dayVol
	return done, vol, ok
}

func barOf(c model.TFCandle) ohlcv {
	return ohlcv{Open: c.Open, High: c.High, Low: c.Low, Close: c.Close, Volume: c.Volume}
}

// atmStrike rounds an index price (paise) to the nearest strike (points).
func atmStrike(pricePaise, step int64) int64 {
	unit := step * 100
	return (pricePaise + unit/2) / unit * step
}

// splitKey splits "exchange:token".
func splitKey(key string) (exch, token string) {
	if i := indexOf(key, ":"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

func indexOf(s, substr string) int {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
