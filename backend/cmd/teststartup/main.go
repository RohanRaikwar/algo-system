package main

import (
	"log"
	"os"
	"strings"
	"time"

	"trading-systemv1/internal/orderexec"
)

func main() {
	log.Println("🧪 Testing Startup Download Configuration")
	log.Println("=" + strings.Repeat("=", 50))

	// Test 1: Startup download enabled (default)
	log.Println("\n📋 Test 1: Startup download ENABLED (default)")
	os.Setenv("INSTRUMENT_MASTER_STARTUP_DOWNLOAD", "true")
	testStartupBehavior("enabled")

	// Test 2: Startup download disabled
	log.Println("\n📋 Test 2: Startup download DISABLED")
	os.Setenv("INSTRUMENT_MASTER_STARTUP_DOWNLOAD", "false")
	testStartupBehavior("disabled")

	// Test 3: Invalid cache with startup download enabled
	log.Println("\n📋 Test 3: Invalid cache with startup download ENABLED")
	os.Setenv("INSTRUMENT_MASTER_STARTUP_DOWNLOAD", "true")
	// Remove cache to simulate invalid/missing cache
	os.RemoveAll(".cache")
	testStartupBehavior("enabled_no_cache")

	// Test 4: Invalid cache with startup download disabled
	log.Println("\n📋 Test 4: Invalid cache with startup download DISABLED")
	os.Setenv("INSTRUMENT_MASTER_STARTUP_DOWNLOAD", "false")
	// Remove cache to simulate invalid/missing cache
	os.RemoveAll(".cache")
	testStartupBehavior("disabled_no_cache")

	log.Println("\n✅ Startup download configuration tests completed")
}

func testStartupBehavior(testName string) {
	// Create a new instance to test startup behavior
	im := orderexec.GetInstrumentMaster()
	
	// Wait a moment for any background operations
	time.Sleep(2 * time.Second)
	
	// Check if loaded
	loaded := im.IsLoaded()
	log.Printf("📊 Test '%s': Loaded = %v", testName, loaded)
	
	if loaded {
		// Try to get a lot size
		if lotSize, err := im.GetLotSize("NIFTY21APR2624350CE"); err == nil {
			log.Printf("✅ Test '%s': NIFTY21APR2624350CE lot size = %d", testName, lotSize)
		} else {
			log.Printf("⚠️  Test '%s': Failed to get lot size: %v", testName, err)
		}
	} else {
		log.Printf("⏸️  Test '%s': Instrument master not loaded (as expected for disabled startup download)", testName)
	}
}