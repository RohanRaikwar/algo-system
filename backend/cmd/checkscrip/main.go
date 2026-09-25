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

	// Search for NIFTY CE option
	symbol := "NIFTY21APR2624350CE"
	log.Printf("\n🔍 Searching for: %s\n", symbol)
	
	res, err := sc.SearchScrip("NFO", symbol)
	if err != nil {
		log.Fatalf("SearchScrip failed: %v", err)
	}

	// Pretty print the full response
	data, _ := json.MarshalIndent(res, "", "  ")
	fmt.Printf("\n📋 Full API Response:\n%s\n", string(data))

	// Extract and display key fields
	if dataArray, ok := res["data"].([]interface{}); ok && len(dataArray) > 0 {
		if first, ok := dataArray[0].(map[string]interface{}); ok {
			fmt.Println("\n📊 Key Fields:")
			fmt.Printf("   Symbol Token: %v\n", first["symboltoken"])
			fmt.Printf("   Trading Symbol: %v\n", first["tradingsymbol"])
			fmt.Printf("   Lot Size: %v\n", first["lotsize"])
			fmt.Printf("   Tick Size: %v\n", first["tick_size"])
			fmt.Printf("   Exchange: %v\n", first["exch_seg"])
		}
	}
}
