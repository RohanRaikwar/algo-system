package main

import (
	"log"
	"os"
	"strings"
	"time"

	"trading-systemv1/internal/orderexec"
)

func main() {
	log.Println("🧪 Testing Cache Configuration Behavior")
	log.Println("=" + strings.Repeat("=", 50))

	// Test with expired cache and startup download disabled
	log.Println("\n📋 Test: Expired cache with startup download DISABLED")
	
	// Set a very short TTL to make cache appear expired
	os.Setenv("INSTRUMENT_MASTER_CACHE_TTL", "1ns") // Extremely short TTL
	os.Setenv("INSTRUMENT_MASTER_STARTUP_DOWNLOAD", "false")
	
	// Create a new instance
	im := orderexec.GetInstrumentMaster()
	
	// Wait for startup operations
	time.Sleep(3 * time.Second)
	
	// Check if loaded
	loaded := im.IsLoaded()
	log.Printf("📊 Loaded with expired cache and startup download disabled: %v", loaded)
	
	if loaded {
		if lotSize, err := im.GetLotSize("NIFTY21APR2624350CE"); err == nil {
			log.Printf("✅ NIFTY21APR2624350CE lot size = %d", lotSize)
		} else {
			log.Printf("⚠️  Failed to get lot size: %v", err)
		}
	}

	// Test EnsureLoaded with timeout
	log.Println("\n📋 Test: EnsureLoaded with timeout")
	if err := im.EnsureLoaded(5 * time.Second); err != nil {
		log.Printf("❌ EnsureLoaded failed: %v", err)
	} else {
		log.Printf("✅ EnsureLoaded succeeded")
	}

	log.Println("\n✅ Cache configuration tests completed")
}