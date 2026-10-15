package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"trading-systemv1/pkg/smartconnect"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(".env"); err != nil {
		log.Fatal("Error loading .env file:", err)
	}

	apiKey := os.Getenv("ANGEL_API_KEY")
	clientCode := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientCode == "" || password == "" || totpSecret == "" {
		log.Fatal("Missing Angel One credentials in .env file")
	}

	fmt.Println("=== Testing Angel One Balance API ===")
	fmt.Println()

	// Create session manager
	sessionManager := smartconnect.NewSessionManager(apiKey, clientCode, password, totpSecret, true)

	// Login
	fmt.Println("🔑 Logging in...")
	if err := sessionManager.Login(); err != nil {
		log.Fatalf("❌ Login failed: %v", err)
	}
	fmt.Println("✅ Login successful")
	fmt.Println()

	// Start session manager
	sessionManager.Start()
	defer sessionManager.Stop()

	// Test RMSLimit API
	fmt.Println("💰 Fetching account balance (RMSLimit)...")
	res, err := sessionManager.RMSLimit()
	if err != nil {
		log.Fatalf("❌ RMSLimit failed: %v", err)
	}

	fmt.Println("✅ RMSLimit response:")
	fmt.Printf("%+v\n", res)
	fmt.Println()

	// Parse response
	status, _ := res["status"].(bool)
	fmt.Printf("Status: %v\n", status)

	if !status {
		msg, _ := res["message"].(string)
		fmt.Printf("❌ Error message: %s\n", msg)
		return
	}

	data, ok := res["data"].(map[string]any)
	if !ok {
		fmt.Println("❌ Unexpected response format")
		return
	}

	fmt.Println("\n📊 Balance Details:")
	fmt.Printf("  Net: %v\n", data["net"])
	fmt.Printf("  Available Cash: %v\n", data["availablecash"])
	fmt.Printf("  Used Margin: %v\n", data["m2munrealized"])
	fmt.Printf("  Utilized Debits: %v\n", data["utiliseddebits"])

	// Wait a bit
	time.Sleep(2 * time.Second)

	fmt.Println("\n✅ Test completed successfully!")
}
