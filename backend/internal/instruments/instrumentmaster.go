// Package orderexec — instrumentmaster.go
//
// InstrumentMaster downloads and caches Angel One's instrument master file
// to fetch lot sizes and other instrument metadata dynamically.
//
// Optimizations:
// - Persistent disk cache to avoid re-downloading on restart
// - Background async loading to not block trading operations
// - Fallback to environment variable if not loaded yet
package instruments

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	cacheFileName         = "instrument_master_cache.json"
	cacheMetadataFileName = "instrument_master_metadata.json"

	// A failed download is retried from loadRetryBase, doubling up to
	// loadRetryMax, until it succeeds.
	loadRetryBase = 30 * time.Second
	loadRetryMax  = 5 * time.Minute
)

// instrumentMasterURL is a var so tests can point it at a local server.
var instrumentMasterURL = "https://margincalculator.angelbroking.com/OpenAPI_File/files/OpenAPIScripMaster.json"

// cacheDir is where the disk cache lives. INSTRUMENT_MASTER_CACHE_DIR points
// it at a persistent volume so a restart does not need a fresh download.
var cacheDir = func() string {
	if dir := os.Getenv("INSTRUMENT_MASTER_CACHE_DIR"); dir != "" {
		return dir
	}
	return ".cache"
}()

// Instrument represents a single instrument from Angel One's master file
type Instrument struct {
	Token          string `json:"token"`
	Symbol         string `json:"symbol"`
	Name           string `json:"name"`
	Expiry         string `json:"expiry"`
	Strike         string `json:"strike"`
	LotSize        string `json:"lotsize"`
	InstrumentType string `json:"instrumenttype"`
	ExchSeg        string `json:"exch_seg"`
	TickSize       string `json:"tick_size"`
}

// CacheMetadata stores information about the cached data
type CacheMetadata struct {
	LastUpdated time.Time `json:"last_updated"`
	Count       int       `json:"count"`
}

// InstrumentMaster manages the instrument master data
type InstrumentMaster struct {
	mu          sync.RWMutex
	instruments map[string]*Instrument // key: symbol
	byToken     map[string]*Instrument // key: exch_seg + ":" + token
	byOption    map[string]*Instrument // key: optionKey(name, seg, expiry, strike, CE/PE)
	lastUpdated time.Time
	cacheTTL    time.Duration
	loading     bool
	loaded      bool
	
	// Daily refresh scheduler
	refreshTime       time.Time // Time of day to refresh (9:12 AM IST)
	schedulerStop     chan struct{}
	schedulerOnce     sync.Once
	startupDownload   bool // Whether to download on startup if cache invalid
}

var (
	globalMaster     *InstrumentMaster
	globalMasterOnce sync.Once
)

// GetInstrumentMaster returns the global singleton instance
func GetInstrumentMaster() *InstrumentMaster {
	globalMasterOnce.Do(func() {
		// Parse refresh time from environment (default: 9:12 AM IST)
		refreshTimeStr := os.Getenv("INSTRUMENT_MASTER_REFRESH_TIME")
		if refreshTimeStr == "" {
			refreshTimeStr = "09:12"
		}
		
		// Parse cache TTL from environment (default: 24 hours)
		cacheTTLStr := os.Getenv("INSTRUMENT_MASTER_CACHE_TTL")
		cacheTTL := 24 * time.Hour
		if cacheTTLStr != "" {
			if parsed, err := time.ParseDuration(cacheTTLStr); err == nil {
				cacheTTL = parsed
			} else {
				log.Printf("[instrumentmaster] ⚠️  Invalid INSTRUMENT_MASTER_CACHE_TTL '%s', using default 24h", cacheTTLStr)
			}
		}
		
		// Parse refresh time (HH:MM format)
		ist := time.FixedZone("IST", 5*3600+30*60)
		refreshTime := time.Date(2000, 1, 1, 9, 12, 0, 0, ist) // default
		
		if parts := strings.Split(refreshTimeStr, ":"); len(parts) == 2 {
			if hour, err1 := strconv.Atoi(parts[0]); err1 == nil {
				if minute, err2 := strconv.Atoi(parts[1]); err2 == nil {
					if hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 {
						refreshTime = time.Date(2000, 1, 1, hour, minute, 0, 0, ist)
					} else {
						log.Printf("[instrumentmaster] ⚠️  Invalid refresh time '%s', using default 09:12", refreshTimeStr)
					}
				} else {
					log.Printf("[instrumentmaster] ⚠️  Invalid refresh time format '%s', using default 09:12", refreshTimeStr)
				}
			} else {
				log.Printf("[instrumentmaster] ⚠️  Invalid refresh time format '%s', using default 09:12", refreshTimeStr)
			}
		} else {
			log.Printf("[instrumentmaster] ⚠️  Invalid refresh time format '%s', using default 09:12", refreshTimeStr)
		}
		
		// Parse startup download setting
		startupDownload := true // default to true
		if startupDownloadStr := os.Getenv("INSTRUMENT_MASTER_STARTUP_DOWNLOAD"); startupDownloadStr != "" {
			if parsed, err := strconv.ParseBool(startupDownloadStr); err == nil {
				startupDownload = parsed
			} else {
				log.Printf("[instrumentmaster] ⚠️  Invalid INSTRUMENT_MASTER_STARTUP_DOWNLOAD '%s', using default true", startupDownloadStr)
			}
		}
		
		globalMaster = &InstrumentMaster{
			instruments:     make(map[string]*Instrument),
			cacheTTL:        cacheTTL,
			refreshTime:     refreshTime,
			schedulerStop:   make(chan struct{}),
			startupDownload: startupDownload,
		}
		
		log.Printf("[instrumentmaster] 📅 Daily refresh configured for %s IST (TTL: %v, startup download: %v)", 
			refreshTime.Format("15:04"), cacheTTL, startupDownload)
		
		// Try to load from disk cache immediately
		globalMaster.loadFromDiskCache()
		// Start background refresh if cache is stale
		go globalMaster.backgroundRefresh()
		// Start daily scheduler
		go globalMaster.startDailyScheduler()
	})
	return globalMaster
}

// startDailyScheduler starts the daily refresh scheduler at the configured time
func (im *InstrumentMaster) startDailyScheduler() {
	ist := time.FixedZone("IST", 5*3600+30*60)
	
	for {
		select {
		case <-im.schedulerStop:
			log.Println("[instrumentmaster] 🛑 Daily scheduler stopped")
			return
		default:
			// Calculate next refresh time based on configured time
			now := time.Now().In(ist)
			nextRefresh := time.Date(now.Year(), now.Month(), now.Day(), 
				im.refreshTime.Hour(), im.refreshTime.Minute(), 0, 0, ist)
			
			// If we've already passed the refresh time today, schedule for tomorrow
			if now.After(nextRefresh) {
				nextRefresh = nextRefresh.Add(24 * time.Hour)
			}
			
			sleepDuration := nextRefresh.Sub(now)
			log.Printf("[instrumentmaster] 📅 Next daily refresh scheduled at %s (in %v)", 
				nextRefresh.Format("02-Jan-2006 15:04:05 MST"), sleepDuration)
			
			// Sleep until the scheduled time
			timer := time.NewTimer(sleepDuration)
			select {
			case <-timer.C:
				// Time to refresh!
				log.Printf("[instrumentmaster] ⏰ Daily scheduled refresh starting at %s IST...", 
					im.refreshTime.Format("15:04"))
				im.loadWithRetry(loadRetryBase, loadRetryMax)
				log.Println("[instrumentmaster] ✅ Daily scheduled refresh completed")
			case <-im.schedulerStop:
				timer.Stop()
				log.Println("[instrumentmaster] 🛑 Daily scheduler stopped during wait")
				return
			}
		}
	}
}

// StopScheduler stops the daily refresh scheduler
func (im *InstrumentMaster) StopScheduler() {
	im.schedulerOnce.Do(func() {
		close(im.schedulerStop)
	})
}

// backgroundRefresh checks if cache is stale and refreshes in background
func (im *InstrumentMaster) backgroundRefresh() {
	im.mu.RLock()
	haveCache := len(im.instruments) > 0
	needsRefresh := time.Since(im.lastUpdated) > im.cacheTTL || !haveCache
	im.mu.RUnlock()

	if !needsRefresh {
		return
	}
	if !im.startupDownload && haveCache {
		log.Println("[instrumentmaster] ⏸️  Startup download disabled, using expired cache")
		return
	}
	log.Println("[instrumentmaster] 🔄 Starting background refresh...")
	im.loadWithRetry(loadRetryBase, loadRetryMax)
}

// loadWithRetry calls Load until it succeeds or the scheduler is stopped.
// Without it one slow download at startup leaves the master empty (lot sizes
// and expiries on fallback) until the next daily refresh.
func (im *InstrumentMaster) loadWithRetry(base, max time.Duration) {
	wait := base
	for {
		err := im.Load()
		if err == nil && im.IsLoaded() {
			return
		}
		if err != nil {
			log.Printf("[instrumentmaster] ❌ Download failed, retrying in %v: %v", wait, err)
		}
		select {
		case <-time.After(wait):
		case <-im.schedulerStop:
			return
		}
		if wait *= 2; wait > max {
			wait = max
		}
	}
}

// loadFromDiskCache loads cached data from disk
func (im *InstrumentMaster) loadFromDiskCache() {
	cacheFilePath := filepath.Join(cacheDir, cacheFileName)
	cacheMetadataFilePath := filepath.Join(cacheDir, cacheMetadataFileName)

	// Check if cache file exists
	if _, err := os.Stat(cacheFilePath); os.IsNotExist(err) {
		log.Println("[instrumentmaster] 💾 No disk cache found")
		return
	}

	// Load metadata
	metadataFile, err := os.ReadFile(cacheMetadataFilePath)
	if err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to read cache metadata: %v", err)
		return
	}

	var metadata CacheMetadata
	if err := json.Unmarshal(metadataFile, &metadata); err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to parse cache metadata: %v", err)
		return
	}

	// Check if cache is still valid
	if time.Since(metadata.LastUpdated) > im.cacheTTL {
		log.Printf("[instrumentmaster] ⏰ Disk cache expired (age: %v)", time.Since(metadata.LastUpdated))
	}

	// Load instruments from cache
	cacheFile, err := os.ReadFile(cacheFilePath)
	if err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to read cache file: %v", err)
		return
	}

	var instruments []Instrument
	if err := json.Unmarshal(cacheFile, &instruments); err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to parse cache file: %v", err)
		return
	}

	// Validate cache data
	if len(instruments) == 0 {
		log.Println("[instrumentmaster] ⚠️  Cache file is empty")
		return
	}

	// Build symbol -> instrument map
	im.mu.Lock()
	im.setInstrumentsLocked(instruments)
	im.lastUpdated = metadata.LastUpdated
	im.loaded = true
	im.mu.Unlock()

	cacheAge := time.Since(metadata.LastUpdated)
	if cacheAge > im.cacheTTL {
		log.Printf("[instrumentmaster] ✅ Loaded %d instruments from expired disk cache (age: %v)", len(instruments), cacheAge)
	} else {
		log.Printf("[instrumentmaster] ✅ Loaded %d instruments from disk cache (age: %v)", len(instruments), cacheAge)
	}
}

// saveToDiskCache saves current data to disk
func (im *InstrumentMaster) saveToDiskCache() error {
	cacheFilePath := filepath.Join(cacheDir, cacheFileName)
	cacheMetadataFilePath := filepath.Join(cacheDir, cacheMetadataFileName)

	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	im.mu.RLock()
	defer im.mu.RUnlock()

	// Convert map to slice
	instruments := make([]Instrument, 0, len(im.instruments))
	for _, inst := range im.instruments {
		instruments = append(instruments, *inst)
	}

	// Save instruments
	data, err := json.Marshal(instruments)
	if err != nil {
		return fmt.Errorf("failed to marshal instruments: %w", err)
	}

	if err := writeFileAtomic(cacheFilePath, data); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	// Save metadata
	metadata := CacheMetadata{
		LastUpdated: im.lastUpdated,
		Count:       len(instruments),
	}
	metadataData, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := writeFileAtomic(cacheMetadataFilePath, metadataData); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	log.Printf("[instrumentmaster] 💾 Saved %d instruments to disk cache", len(instruments))
	return nil
}

// writeFileAtomic writes via a temp file and rename, so a crash mid-write
// never leaves a truncated cache on the volume.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load downloads and parses the instrument master file
func (im *InstrumentMaster) Load() error {
	im.mu.Lock()
	if im.loading {
		im.mu.Unlock()
		log.Println("[instrumentmaster] ⏳ Already loading, skipping duplicate request")
		return nil
	}
	im.loading = true
	im.mu.Unlock()

	defer func() {
		im.mu.Lock()
		im.loading = false
		im.mu.Unlock()
	}()

	log.Println("[instrumentmaster] 📥 Downloading instrument master file...")
	startTime := time.Now()

	client := &http.Client{Timeout: 3 * time.Minute} // large file; 60s timed out on Railway
	resp, err := client.Get(instrumentMasterURL)
	if err != nil {
		return fmt.Errorf("failed to download instrument master: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("instrument master download failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read instrument master: %w", err)
	}

	var instruments []Instrument
	if err := json.Unmarshal(body, &instruments); err != nil {
		return fmt.Errorf("failed to parse instrument master: %w", err)
	}

	// Build symbol -> instrument map
	im.mu.Lock()
	oldCount := len(im.instruments)
	im.setInstrumentsLocked(instruments)
	im.lastUpdated = time.Now()
	im.loaded = true
	im.mu.Unlock()

	downloadTime := time.Since(startTime)
	newCount := len(instruments)
	
	if oldCount > 0 {
		log.Printf("[instrumentmaster] ✅ Refreshed %d instruments (was %d) in %v", newCount, oldCount, downloadTime)
	} else {
		log.Printf("[instrumentmaster] ✅ Loaded %d instruments in %v", newCount, downloadTime)
	}

	// Save to disk cache synchronously (to ensure it completes)
	if err := im.saveToDiskCache(); err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to save disk cache: %v", err)
	}

	return nil
}

// GetLotSize returns the lot size for a given symbol
func (im *InstrumentMaster) GetLotSize(symbol string) (int64, error) {
	im.mu.RLock()
	loaded := im.loaded
	inst, ok := im.instruments[symbol]
	im.mu.RUnlock()

	// If not loaded yet, return error (caller should use fallback)
	if !loaded {
		return 0, fmt.Errorf("instrument master not loaded yet (use fallback)")
	}

	if !ok {
		return 0, fmt.Errorf("symbol %s not found in instrument master", symbol)
	}

	lotSize, err := strconv.ParseInt(inst.LotSize, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid lot size for %s: %s", symbol, inst.LotSize)
	}

	return lotSize, nil
}

// GetInstrument returns full instrument details for a given symbol
func (im *InstrumentMaster) GetInstrument(symbol string) (*Instrument, error) {
	im.mu.RLock()
	loaded := im.loaded
	inst, ok := im.instruments[symbol]
	im.mu.RUnlock()

	// If not loaded yet, return error (caller should use fallback)
	if !loaded {
		return nil, fmt.Errorf("instrument master not loaded yet (use fallback)")
	}

	if !ok {
		return nil, fmt.Errorf("symbol %s not found in instrument master", symbol)
	}

	return inst, nil
}

// setInstrumentsLocked replaces both indexes. Caller holds im.mu for writing.
func (im *InstrumentMaster) setInstrumentsLocked(instruments []Instrument) {
	im.instruments = make(map[string]*Instrument, len(instruments))
	im.byToken = make(map[string]*Instrument, len(instruments))
	im.byOption = make(map[string]*Instrument)
	for i := range instruments {
		inst := &instruments[i]
		im.instruments[inst.Symbol] = inst
		im.byToken[inst.ExchSeg+":"+inst.Token] = inst
		if k, ok := instOptionKey(inst); ok {
			im.byOption[k] = inst
		}
	}
}

// optionKey identifies an option contract independent of its trading
// symbol, whose format differs by exchange (NFO "NIFTY03NOV2621250PE",
// BFO weekly "SENSEX26N0564600CE"). strike is in index points.
func optionKey(name, exchSeg string, expiry time.Time, strike int64, optionType string) string {
	return name + "|" + exchSeg + "|" + strings.ToUpper(expiry.In(istZone).Format("02Jan2006")) +
		"|" + strconv.FormatInt(strike, 10) + "|" + optionType
}

// instOptionKey is optionKey for a master row; ok=false when it is not an
// index option. The master's strike is in paise ("6460000.000000").
func instOptionKey(inst *Instrument) (string, bool) {
	if inst.InstrumentType != "OPTIDX" {
		return "", false
	}
	var optType string
	switch {
	case strings.HasSuffix(inst.Symbol, "CE"):
		optType = "CE"
	case strings.HasSuffix(inst.Symbol, "PE"):
		optType = "PE"
	default:
		return "", false
	}
	paise, err := strconv.ParseFloat(strings.TrimSpace(inst.Strike), 64)
	if err != nil || paise <= 0 {
		return "", false
	}
	exp, ok := parseMasterExpiry(inst.Expiry)
	if !ok {
		return "", false
	}
	return optionKey(inst.Name, inst.ExchSeg, exp, int64(paise+0.5)/100, optType), true
}

// FindOption returns the listed option on name (e.g. "SENSEX") on exchSeg
// ("BFO") with that expiry day, strike (index points) and type ("CE"/"PE"),
// or nil when it is not listed or the master is not loaded.
func (im *InstrumentMaster) FindOption(name, exchSeg string, expiry time.Time, strike int64, optionType string) *Instrument {
	im.mu.RLock()
	defer im.mu.RUnlock()
	if !im.loaded {
		return nil
	}
	return im.byOption[optionKey(name, exchSeg, expiry, strike, strings.ToUpper(optionType))]
}

// StrikeStep returns the smallest gap (index points) between listed
// strikes of name's options on exchSeg for the nearest listed expiry, or 0
// when unknown.
func (im *InstrumentMaster) StrikeStep(name, exchSeg string, from time.Time) int64 {
	exps := im.OptionExpiriesOn(name, exchSeg, from)
	if len(exps) == 0 {
		return 0
	}
	want := strings.ToUpper(exps[0].Format("02Jan2006"))
	im.mu.RLock()
	defer im.mu.RUnlock()
	seen := map[int64]bool{}
	var strikes []int64
	for _, inst := range im.instruments {
		if inst.Name != name || inst.ExchSeg != exchSeg || inst.InstrumentType != "OPTIDX" ||
			strings.ToUpper(inst.Expiry) != want {
			continue
		}
		paise, err := strconv.ParseFloat(strings.TrimSpace(inst.Strike), 64)
		if err != nil || paise <= 0 {
			continue
		}
		k := int64(paise+0.5) / 100
		if !seen[k] {
			seen[k] = true
			strikes = append(strikes, k)
		}
	}
	sort.Slice(strikes, func(i, j int) bool { return strikes[i] < strikes[j] })
	var step int64
	for i := 1; i < len(strikes); i++ {
		if d := strikes[i] - strikes[i-1]; d > 0 && (step == 0 || d < step) {
			step = d
		}
	}
	return step
}

// SymbolForToken returns the trading symbol (e.g. "NIFTY06OCT2622500PE") of
// a token on an exchange segment ("NFO"), or "" when unknown or not loaded.
// Tokens are only unique within a segment, so the segment is required.
func (im *InstrumentMaster) SymbolForToken(exchSeg, token string) string {
	if token == "" {
		return ""
	}
	im.mu.RLock()
	defer im.mu.RUnlock()
	if inst, ok := im.byToken[exchSeg+":"+token]; ok {
		return inst.Symbol
	}
	return ""
}

// OptionExpiries is OptionExpiriesOn for NFO.
func (im *InstrumentMaster) OptionExpiries(name string, from time.Time) []time.Time {
	return im.OptionExpiriesOn(name, "NFO", from)
}

// OptionExpiriesOn returns the distinct expiry dates (IST midnight, sorted)
// of the options on name (e.g. "NIFTY") on exchSeg ("NFO", "BFO") on or
// after from's IST date, or nil when the master is not loaded. These are the
// real listed expiries, so a holiday-shifted week (Tue 20 Oct 2026 → Mon 19
// Oct) is reported as listed.
func (im *InstrumentMaster) OptionExpiriesOn(name, exchSeg string, from time.Time) []time.Time {
	im.mu.RLock()
	defer im.mu.RUnlock()
	if !im.loaded {
		return nil
	}
	from = from.In(istZone)
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, istZone)
	seen := make(map[string]bool)
	var out []time.Time
	for _, inst := range im.instruments {
		if inst.Name != name || inst.ExchSeg != exchSeg || seen[inst.Expiry] ||
			!(strings.HasSuffix(inst.Symbol, "CE") || strings.HasSuffix(inst.Symbol, "PE")) {
			continue
		}
		seen[inst.Expiry] = true
		exp, err := time.ParseInLocation("02Jan2006", inst.Expiry, istZone)
		if err != nil || exp.Before(day) {
			continue
		}
		out = append(out, exp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// IsLoaded returns true if instrument master is loaded
func (im *InstrumentMaster) IsLoaded() bool {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.loaded
}

// EnsureLoaded ensures the instrument master is loaded, downloading if necessary
// This is a blocking call that should be used during startup
func (im *InstrumentMaster) EnsureLoaded(timeout time.Duration) error {
	// Check if already loaded
	if im.IsLoaded() {
		return nil
	}

	log.Println("[instrumentmaster] 🔄 Ensuring instrument master is loaded...")
	
	// Try to load from cache first
	im.loadFromDiskCache()
	
	// If still not loaded, download immediately
	if !im.IsLoaded() {
		log.Println("[instrumentmaster] 📥 Cache not available, downloading instrument master...")
		if err := im.Load(); err != nil {
			return fmt.Errorf("failed to load instrument master: %w", err)
		}
	}
	
	// Wait for loading to complete with timeout
	return im.WaitForLoad(timeout)
}

// WaitForLoad waits for instrument master to load (with timeout)
func (im *InstrumentMaster) WaitForLoad(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if im.IsLoaded() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for instrument master to load")
}

// ForceRefresh forces an immediate refresh of the instrument master
func (im *InstrumentMaster) ForceRefresh() error {
	return im.Load()
}

// ForceDailyRefresh forces an immediate daily refresh (same as ForceRefresh but with different logging)
func (im *InstrumentMaster) ForceDailyRefresh() error {
	log.Println("[instrumentmaster] 🔄 Manual daily refresh triggered...")
	if err := im.Load(); err != nil {
		log.Printf("[instrumentmaster] ❌ Manual daily refresh failed: %v", err)
		return err
	}
	log.Println("[instrumentmaster] ✅ Manual daily refresh completed successfully")
	return nil
}
