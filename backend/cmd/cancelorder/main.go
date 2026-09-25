package main

import (
	"fmt"
	"log"
	"os"

	"trading-systemv1/internal/realmoney"
	"trading-systemv1/pkg/smartconnect"

	"github.com/joho/godotenv"
	"github.com/pquerna/otp/totp"
	"time"
)

func main() {
	args := realmoney.Confirm("cancels a real order")

	// Load environment
	envFile := ""
	for _, path := range []string{".env.prod", "../.env.prod", ".env", "../.env"} {
		if _, err := os.Stat(path); err == nil {
			envFile = path
			break
		}
	}
	
	if envFile == "" {
		log.Fatal("No .env file found")
	}
	
	if err := godotenv.Load(envFile); err != nil {
		log.Fatalf("Failed to load %s: %v", envFile, err)
	}

	if len(args) < 1 {
		log.Fatal("Usage: go run backend/cmd/cancelorder/main.go " + realmoney.Flag + " <ORDER_ID>")
	}

	orderID := args[0]

	apiKey := os.Getenv("ANGEL_API_KEY")
	clientID := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientID == "" || password == "" || totpSecret == "" {
		log.Fatal("Missing Angel One credentials")
	}

	// Create client and login
	sc := smartconnect.NewSmartConnect(smartconnect.Config{
		APIKey: apiKey,
	})

	totpCode, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		log.Fatalf("TOTP failed: %v", err)
	}

	_, err = sc.GenerateSession(clientID, password, totpCode)
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}

	log.Printf("✅ Logged in successfully")

	// Cancel order
	log.Printf("🗑️  Cancelling order: %s", orderID)

	result, err := sc.CancelOrder(orderID, "NORMAL")
	if err != nil {
		log.Fatalf("❌ Cancel failed: %v", err)
	}

	log.Printf("✅ Order cancelled successfully")
	fmt.Printf("Result: %+v\n", result)
}
