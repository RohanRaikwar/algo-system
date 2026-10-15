package main

import (
	"fmt"
	"log"
	"time"
	"trading-systemv1/internal/orderexec"
)

func main() {
	log.Println("🧪 Testing Instrument Master Lot Size Fetching")
	log.Println("=" + string(make([]byte, 50)))

	im := orderexec.GetInstrumentMaster()

	// Check if loaded from cache
	if im.IsLoaded() {
		log.Println("✅ Loaded from disk cache")
	} else {
		log.Println("⏳ Waiting for background load (max 30s)...")
		if err := im.WaitForLoad(30 * time.Second); err != nil {
			log.Fatalf("❌ Failed to load: %v", err)
		}
		log.Println("✅ Loaded successfully")
	}

	// Test symbols
	testSymbols := []string{
		"NIFTY21APR2624350CE",
		"NIFTY21APR2624350PE",
		"NIFTY21APR2624300CE",
		"BANKNIFTY21APR2653000CE",
	}

	for _, symbol := range testSymbols {
		lotSize, err := im.GetLotSize(symbol)
		if err != nil {
			log.Printf("❌ %s: %v", symbol, err)
		} else {
			log.Printf("✅ %s: lot size = %d", symbol, lotSize)
		}
	}

	// Test full instrument details
	log.Println("\n📋 Full Instrument Details:")
	inst, err := im.GetInstrument("NIFTY21APR2624350CE")
	if err != nil {
		log.Fatalf("Failed to get instrument: %v", err)
	}

	fmt.Printf("\nSymbol: %s\n", inst.Symbol)
	fmt.Printf("Token: %s\n", inst.Token)
	fmt.Printf("Name: %s\n", inst.Name)
	fmt.Printf("Expiry: %s\n", inst.Expiry)
	fmt.Printf("Strike: %s\n", inst.Strike)
	fmt.Printf("Lot Size: %s\n", inst.LotSize)
	fmt.Printf("Exchange: %s\n", inst.ExchSeg)
	fmt.Printf("Instrument Type: %s\n", inst.InstrumentType)
}
