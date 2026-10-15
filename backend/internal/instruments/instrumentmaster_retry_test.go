package instruments

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A failed startup download must be retried until it succeeds, and the
// result written to the disk cache so the next restart needs no download.
func TestInstrumentMasterLoadWithRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`[{"token":"47141","symbol":"NIFTY19OCT2622650CE","name":"NIFTY","expiry":"19OCT2026","strike":"2265000.000000","lotsize":"65","instrumenttype":"OPTIDX","exch_seg":"NFO","tick_size":"5.000000"}]`))
	}))
	defer srv.Close()

	oldURL, oldDir := instrumentMasterURL, cacheDir
	instrumentMasterURL, cacheDir = srv.URL, t.TempDir()
	defer func() { instrumentMasterURL, cacheDir = oldURL, oldDir }()

	im := &InstrumentMaster{
		instruments:     make(map[string]*Instrument),
		cacheTTL:        24 * time.Hour,
		schedulerStop:   make(chan struct{}),
		startupDownload: true,
	}

	done := make(chan struct{})
	go func() { im.loadWithRetry(time.Millisecond, 5*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		im.StopScheduler()
		t.Fatal("loadWithRetry did not finish")
	}

	if got := calls.Load(); got != 3 {
		t.Fatalf("downloads = %d, want 3 (two failures, one success)", got)
	}
	if lot, err := im.GetLotSize("NIFTY19OCT2622650CE"); err != nil || lot != 65 {
		t.Fatalf("GetLotSize = %d, %v; want 65", lot, err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, cacheFileName)); err != nil {
		t.Fatalf("disk cache not written: %v", err)
	}

	// A fresh master reads the cache without downloading.
	im2 := &InstrumentMaster{instruments: make(map[string]*Instrument), cacheTTL: 24 * time.Hour}
	im2.loadFromDiskCache()
	if !im2.IsLoaded() || calls.Load() != 3 {
		t.Fatalf("cache reload: loaded=%v downloads=%d", im2.IsLoaded(), calls.Load())
	}
}

// Stopping the scheduler ends the retry loop even if downloads keep failing.
func TestInstrumentMasterLoadWithRetryStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	oldURL, oldDir := instrumentMasterURL, cacheDir
	instrumentMasterURL, cacheDir = srv.URL, t.TempDir()
	defer func() { instrumentMasterURL, cacheDir = oldURL, oldDir }()

	im := &InstrumentMaster{instruments: make(map[string]*Instrument), schedulerStop: make(chan struct{})}
	done := make(chan struct{})
	go func() { im.loadWithRetry(time.Millisecond, time.Millisecond); close(done) }()
	time.Sleep(20 * time.Millisecond)
	im.StopScheduler()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loadWithRetry ignored schedulerStop")
	}
}
