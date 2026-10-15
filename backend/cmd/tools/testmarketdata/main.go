package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

func main() {
	// Get credentials from environment
	apiKey := os.Getenv("ANGEL_API_KEY")
	clientCode := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientCode == "" || password == "" || totpSecret == "" {
		log.Fatal("Missing Angel One credentials")
	}

	// Generate TOTP
	totpCode, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		log.Fatalf("TOTP generation failed: %v", err)
	}

	// Initialize SmartConnect
	sc := smartconnect.NewSmartConnect(smartconnect.Config{
		APIKey: apiKey,
		Debug:  false,
	})

	// Login
	log.Println("🔑 Logging in to Angel One...")
	_, err = sc.GenerateSession(clientCode, password, totpCode)
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}
	log.Println("✅ Logged in successfully")

	// Test GetMarketData with NIFTY option token
	token := "63434" // NIFTY21APR2624350CE
	log.Printf("\n🔍 Fetching market data for token: %s\n", token)
	
	// Mode: FULL, LTP, OHLC, QUOTE
	// exchangeTokens format: {"NFO": ["token1", "token2"]}
	exchangeTokens := map[string][]string{
		"NFO": {token},
	}
	
	res, err := sc.GetMarketData("FULL", exchangeTokens)
	if err != nil {
		log.Fatalf("GetMarketData failed: %v", err)
	}

	// Pretty print the full response
	data, _ := json.MarshalIndent(res, "", "  ")
	fmt.Printf("\n📋 Full Market Data Response:\n%s\n", string(data))

	// Extract and display key fields
	if dataMap, ok := res["data"].(map[string]interface{}); ok {
		if fetched, ok := dataMap["fetched"].([]interface{}); ok && len(fetched) > 0 {
			if first, ok := fetched[0].(map[string]interface{}); ok {
				fmt.Println("\n📊 Key Fields:")
				fmt.Printf("   Symbol Token: %v\n", first["symboltoken"])
				fmt.Printf("   Trading Symbol: %v\n", first["tradingsymbol"])
				fmt.Printf("   Lot Size: %v\n", first["lotsize"])
				fmt.Printf("   Tick Size: %v\n", first["ticksize"])
				fmt.Printf("   Exchange: %v\n", first["exchange"])
				fmt.Printf("   LTP: %v\n", first["ltp"])
			}
		}
	}
}
