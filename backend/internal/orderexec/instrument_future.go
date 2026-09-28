package orderexec

import (
	"fmt"
	"strings"
	"time"
)

// NearestFuture returns the nearest-expiry index future for name (e.g.
// "NIFTY") on NFO that has not expired before now's IST date.
func (im *InstrumentMaster) NearestFuture(name string, now time.Time) (*Instrument, error) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	if !im.loaded {
		return nil, fmt.Errorf("instrument master not loaded yet")
	}
	return nearestFuture(im.instruments, name, now)
}

func nearestFuture(instruments map[string]*Instrument, name string, now time.Time) (*Instrument, error) {
	n := now.In(istZone)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, istZone)
	var best *Instrument
	var bestExp time.Time
	for _, inst := range instruments {
		if inst.Name != name || inst.InstrumentType != "FUTIDX" || inst.ExchSeg != "NFO" || inst.Token == "" {
			continue
		}
		exp, ok := parseMasterExpiry(inst.Expiry)
		if !ok || exp.Before(today) {
			continue
		}
		if best == nil || exp.Before(bestExp) {
			best, bestExp = inst, exp
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no live %s index future in instrument master", name)
	}
	return best, nil
}

// parseMasterExpiry parses Angel's "28OCT2025" expiry format.
func parseMasterExpiry(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 9 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("02Jan2006", s[:2]+s[2:3]+strings.ToLower(s[3:5])+s[5:], istZone)
	return t, err == nil
}
