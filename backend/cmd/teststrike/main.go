package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trading-systemv1/internal/orderexec"
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
		log.Fatal("Missing Angel One credentials in environment variables")
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

	// Create StrikePicker
	picker := orderexec.NewStrikePicker(sc)

	// Test with index price 24350
	indexPrice := int64(24350 * 100) // Convert to paise: 2435000
	log.Printf("\n📊 Testing strike resolution with index price: ₹%.2f (paise: %d)", float64(indexPrice)/100, indexPrice)

	// Resolve ATM strikes
	log.Println("\n🎯 Resolving ATM strikes...")
	success := picker.ResolveATM(indexPrice)

	if !success {
		log.Fatal("❌ Strike resolution failed")
	}

	// Get resolved strikes
	callStrike := picker.GetCallToken()
	putStrike := picker.GetPutToken()

	// Display results
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("✅ STRIKE RESOLUTION SUCCESSFUL")
	fmt.Println(strings.Repeat("═", 70))
	
	fmt.Printf("\n📈 CALL (CE) Strike:\n")
	fmt.Printf("   Symbol:   %s\n", callStrike.Symbol)
	fmt.Printf("   Token:    %s\n", callStrike.Token)
	fmt.Printf("   Strike:   %d\n", callStrike.Strike)
	fmt.Printf("   Lot Size: %d\n", callStrike.LotSize)
	
	fmt.Printf("\n📉 PUT (PE) Strike:\n")
	fmt.Printf("   Symbol:   %s\n", putStrike.Symbol)
	fmt.Printf("   Token:    %s\n", putStrike.Token)
	fmt.Printf("   Strike:   %d\n", putStrike.Strike)
	fmt.Printf("   Lot Size: %d\n", putStrike.LotSize)
	
	fmt.Printf("\n💼 Trading Details:\n")
	fmt.Printf("   ATM Strike:     %d\n", callStrike.Strike)
	fmt.Printf("   Lot Size:       %d\n", callStrike.LotSize)
	fmt.Printf("   1 Lot Value:    %d shares\n", callStrike.LotSize)
	fmt.Printf("   Index Price:    ₹%.2f\n", float64(indexPrice)/100)
	
	fmt.Println("\n" + strings.Repeat("═", 70))
}
