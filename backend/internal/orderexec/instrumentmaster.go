// Package orderexec — instrumentmaster.go
//
// InstrumentMaster downloads and caches Angel One's instrument master file
// to fetch lot sizes and other instrument metadata dynamically.
//
// Optimizations:
// - Persistent disk cache to avoid re-downloading on restart
// - Background async loading to not block trading operations
// - Fallback to environment variable if not loaded yet
package orderexec

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	instrumentMasterURL   = "https://margincalculator.angelbroking.com/OpenAPI_File/files/OpenAPIScripMaster.json"
	cacheDir              = ".cache"
	cacheFileName         = "instrument_master_cache.json"
	cacheMetadataFileName = "instrument_master_metadata.json"
)

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
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Daily scheduled refresh failed: %v", err)
				} else {
					log.Println("[instrumentmaster] ✅ Daily scheduled refresh completed successfully")
				}
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
	needsRefresh := time.Since(im.lastUpdated) > im.cacheTTL || len(im.instruments) == 0
	im.mu.RUnlock()

	if needsRefresh {
		log.Println("[instrumentmaster] 🔄 Starting background refresh...")
		if err := im.Load(); err != nil {
			log.Printf("[instrumentmaster] ⚠️  Background refresh failed: %v", err)
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
		if im.startupDownload {
			log.Println("[instrumentmaster] 📥 Startup download enabled, downloading immediately...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, skipping download")
		}
		return
	}

	// Load metadata
	metadataFile, err := os.ReadFile(cacheMetadataFilePath)
	if err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to read cache metadata: %v", err)
		if im.startupDownload {
			log.Println("[instrumentmaster] 🔄 Cache metadata invalid, downloading fresh data...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, skipping download")
		}
		return
	}

	var metadata CacheMetadata
	if err := json.Unmarshal(metadataFile, &metadata); err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to parse cache metadata: %v", err)
		if im.startupDownload {
			log.Println("[instrumentmaster] 🔄 Cache metadata corrupted, downloading fresh data...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, skipping download")
		}
		return
	}

	// Check if cache is still valid
	if time.Since(metadata.LastUpdated) > im.cacheTTL {
		log.Printf("[instrumentmaster] ⏰ Disk cache expired (age: %v)", time.Since(metadata.LastUpdated))
		if im.startupDownload {
			log.Println("[instrumentmaster] 📥 Downloading fresh data...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, using expired cache")
		}
		// Continue to load expired cache if startup download is disabled
		if !im.startupDownload {
			// Load expired cache anyway
		} else {
			return
		}
	}

	// Load instruments from cache
	cacheFile, err := os.ReadFile(cacheFilePath)
	if err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to read cache file: %v", err)
		if im.startupDownload {
			log.Println("[instrumentmaster] 🔄 Cache file unreadable, downloading fresh data...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, skipping download")
		}
		return
	}

	var instruments []Instrument
	if err := json.Unmarshal(cacheFile, &instruments); err != nil {
		log.Printf("[instrumentmaster] ⚠️  Failed to parse cache file: %v", err)
		if im.startupDownload {
			log.Println("[instrumentmaster] 🔄 Cache file corrupted, downloading fresh data...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, skipping download")
		}
		return
	}

	// Validate cache data
	if len(instruments) == 0 {
		log.Println("[instrumentmaster] ⚠️  Cache file is empty")
		if im.startupDownload {
			log.Println("[instrumentmaster] 📥 Downloading fresh data...")
			go func() {
				if err := im.Load(); err != nil {
					log.Printf("[instrumentmaster] ❌ Startup download failed: %v", err)
				}
			}()
		} else {
			log.Println("[instrumentmaster] ⏸️  Startup download disabled, skipping download")
		}
		return
	}

	// Build symbol -> instrument map
	im.mu.Lock()
	im.instruments = make(map[string]*Instrument, len(instruments))
	for i := range instruments {
		inst := &instruments[i]
		im.instruments[inst.Symbol] = inst
	}
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

	if err := os.WriteFile(cacheFilePath, data, 0644); err != nil {
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

	if err := os.WriteFile(cacheMetadataFilePath, metadataData, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	log.Printf("[instrumentmaster] 💾 Saved %d instruments to disk cache", len(instruments))
	return nil
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

	client := &http.Client{Timeout: 60 * time.Second} // Increased timeout for large file
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
	im.instruments = make(map[string]*Instrument, len(instruments))
	for i := range instruments {
		inst := &instruments[i]
		im.instruments[inst.Symbol] = inst
	}
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
