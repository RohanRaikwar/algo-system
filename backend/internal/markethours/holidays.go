package markethours

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// pre-computed holiday lookup: date string → holiday name
var (
	holidaySet map[string]string
	holidayMu  sync.RWMutex
)

func init() {
	// Start with empty set — holidays are loaded via InitHolidays from
	// the NSE API or cached JSON. No hardcoded fallback.
	holidaySet = make(map[string]string, 30)
}

// isCacheFresh returns true if the cache was fetched AFTER the most recent 8 AM IST.
// NSE can update the calendar mid-year, so we refresh daily at 8 AM.
func isCacheFresh(fetchedAt string) bool {
	if fetchedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return false
	}
	now := time.Now().In(IST)

	// Determine the most recent 8:00 AM IST
	recent8AM := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, IST)
	if now.Before(recent8AM) {
		recent8AM = recent8AM.AddDate(0, 0, -1) // most recent 8 AM was yesterday
	}

	return t.After(recent8AM)
}

// InitHolidays loads holidays with cache-first strategy:
//  1. Cached JSON from config directory (if current year and fresh → done)
//  2. Live fetch from NSE API
//  3. Stale cache as last resort (better than nothing)
//
// No hardcoded fallback — only real NSE data is used.
// Call this once at startup from every service that uses markethours.
func InitHolidays(configDir string) {
	currentYear := time.Now().In(IST).Year()

	// Try cached JSON first — avoids hitting NSE on every restart
	cache, err := LoadHolidayCache(configDir)
	fresh := isCacheFresh(cache.FetchedAt)

	if err == nil && cache.Year == currentYear && len(cache.Holidays) > 0 && fresh {
		log.Printf("[markethours] 📦 loaded %d holidays from cache (year %d, fresh enough)", len(cache.Holidays), cache.Year)
		applyHolidays(cache.Holidays)
		return
	}

	// Cache missing or stale — fetch from NSE
	if err != nil && !os.IsNotExist(err) {
		log.Printf("[markethours] ⚠️  cache read failed: %v", err)
	} else if cache.Year != currentYear {
		log.Printf("[markethours] 📅 cache year %d != current year %d — fetching fresh data", cache.Year, currentYear)
	} else if !fresh {
		log.Printf("[markethours] ⏳ cache is older than most recent 8 AM — fetching fresh data for mid-year updates")
	}

	holidays, fetchErr := FetchNSEHolidays()
	if fetchErr == nil && len(holidays) > 0 {
		log.Printf("[markethours] ✅ fetched %d holidays from NSE API", len(holidays))
		applyHolidays(holidays)
		if err := SaveHolidayCache(configDir, holidays); err != nil {
			log.Printf("[markethours] ⚠️  failed to cache holidays: %v", err)
		}
		return
	}
	if fetchErr != nil {
		log.Printf("[markethours] ⚠️  NSE API fetch failed: %v", fetchErr)
	}

	// Last resort: use stale cache if it exists (better than empty)
	if err == nil && len(cache.Holidays) > 0 {
		log.Printf("[markethours] 📋 using stale cache (%d holidays, year %d, fetched %s) — NSE API unavailable",
			len(cache.Holidays), cache.Year, cache.FetchedAt)
		applyHolidays(cache.Holidays)
		return
	}

	log.Printf("[markethours] ⚠️  NO holiday data available — all days will be treated as trading days until NSE API responds")
}

// StartBackgroundRefresher starts a goroutine that checks every hour to see
// if the holiday cache has become stale (passed 8 AM). If so, it fetches new data.
func StartBackgroundRefresher(ctx context.Context, configDir string) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cache, err := LoadHolidayCache(configDir)
				if err == nil && len(cache.Holidays) > 0 && !isCacheFresh(cache.FetchedAt) {
					log.Printf("[markethours] ⏳ background refresher: cache is stale, fetching fresh holidays")
					holidays, fetchErr := FetchNSEHolidays()
					if fetchErr == nil && len(holidays) > 0 {
						log.Printf("[markethours] ✅ background refresher: fetched %d holidays", len(holidays))
						applyHolidays(holidays)
						if err := SaveHolidayCache(configDir, holidays); err != nil {
							log.Printf("[markethours] ⚠️  failed to cache holidays: %v", err)
						}
					}
				}
				// Also try fetching if no cache exists at all
				if err != nil {
					log.Printf("[markethours] ⏳ background refresher: no cache found, attempting NSE fetch")
					holidays, fetchErr := FetchNSEHolidays()
					if fetchErr == nil && len(holidays) > 0 {
						log.Printf("[markethours] ✅ background refresher: fetched %d holidays (first time)", len(holidays))
						applyHolidays(holidays)
						if err := SaveHolidayCache(configDir, holidays); err != nil {
							log.Printf("[markethours] ⚠️  failed to cache holidays: %v", err)
						}
					}
				}
			}
		}
	}()
}

// applyHolidays replaces the in-memory holiday set (thread-safe).
func applyHolidays(holidays []HolidayEntry) {
	newSet := make(map[string]string, len(holidays))
	for _, h := range holidays {
		newSet[h.Date] = h.Name
	}
	holidayMu.Lock()
	holidaySet = newSet
	holidayMu.Unlock()
}

// IsHoliday returns true if the date (in IST) is an NSE holiday.
func IsHoliday(t time.Time) bool {
	ist := t.In(IST)
	key := ist.Format("2006-01-02")
	holidayMu.RLock()
	_, ok := holidaySet[key]
	holidayMu.RUnlock()
	return ok
}

// TodayHoliday returns the holiday name and whether today is a holiday.
func TodayHoliday(t time.Time) (string, bool) {
	ist := t.In(IST)
	key := ist.Format("2006-01-02")
	holidayMu.RLock()
	name, ok := holidaySet[key]
	holidayMu.RUnlock()
	return name, ok
}

// NextTradingDay returns the next day the market will be open (skipping weekends and holidays).
func NextTradingDay(t time.Time) time.Time {
	ist := t.In(IST)
	d := time.Date(ist.Year(), ist.Month(), ist.Day(), 0, 0, 0, 0, IST)
	for i := 0; i < 15; i++ { // max 15 days ahead
		d = d.AddDate(0, 0, 1)
		if IsTradingDay(d) {
			return d
		}
	}
	// Shouldn't get here, but fallback to next weekday
	return d
}

// GetConfigDir returns the config directory relative to the working directory.
func GetConfigDir() string {
	// Check if config/ exists in current directory, otherwise try relative paths
	candidates := []string{"config", "../config", "../../config"}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	// Create config/ in current directory
	os.MkdirAll("config", 0755)
	abs, _ := filepath.Abs("config")
	return abs
}

func dateKey(year int, month time.Month, day int) string {
	return time.Date(year, month, day, 0, 0, 0, 0, IST).Format("2006-01-02")
}
