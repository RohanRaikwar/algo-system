package main

import (
	"log"
	"time"
	"trading-systemv1/internal/orderexec"
)

func main() {
	log.Println("🧪 Testing Daily Refresh Scheduler")
	log.Println("=" + string(make([]byte, 50)))

	// Get instrument master instance
	im := orderexec.GetInstrumentMaster()

	// Wait a moment for scheduler to start
	time.Sleep(2 * time.Second)

	// Test manual daily refresh
	log.Println("🔄 Testing manual daily refresh...")
	if err := im.ForceDailyRefresh(); err != nil {
		log.Printf("❌ Manual daily refresh failed: %v", err)
	}

	// Show current status
	log.Printf("📊 Instrument master loaded: %v", im.IsLoaded())
	
	// Test lot size after refresh
	if lotSize, err := im.GetLotSize("NIFTY21APR2624350CE"); err != nil {
		log.Printf("❌ Failed to get lot size: %v", err)
	} else {
		log.Printf("✅ NIFTY21APR2624350CE lot size after refresh: %d", lotSize)
	}

	log.Println("✅ Daily refresh test completed")
}