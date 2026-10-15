package main

import (
	"log"
	"os"
	"time"

	"trading-systemv1/pkg/smartconnect"

	"github.com/joho/godotenv"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("[test] Session Manager Refresh Test")

	// Load environment
	if err := godotenv.Load("../../.env"); err != nil {
		log.Printf("[test] ⚠️  .env not found: %v", err)
	}

	apiKey := os.Getenv("ANGEL_API_KEY")
	clientID := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientID == "" || password == "" || totpSecret == "" {
		log.Fatal("[test] ❌ missing Angel One credentials in environment")
	}

	// Create session manager
	sm := smartconnect.NewSessionManager(apiKey, clientID, password, totpSecret, true)
	log.Println("[test] 📦 session manager created")

	// Login
	if err := sm.Login(); err != nil {
		log.Fatalf("[test] ❌ login failed: %v", err)
	}
	log.Println("[test] ✅ initial login successful")

	// Start proactive refresh loop
	sm.Start()
	log.Println("[test] 🔄 proactive refresh loop started")

	// Monitor session health for 25 minutes (to see at least one refresh)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	timeout := time.After(25 * time.Minute)

	for {
		select {
		case <-timeout:
			log.Println("[test] ⏱️  test completed (25 minutes)")
			sm.Stop()
			return

		case <-ticker.C:
			// Get session info
			info := sm.GetSessionInfo()
			log.Printf("[test] 📊 Session Status:")
			log.Printf("  - Valid: %v", info["valid"])
			log.Printf("  - Age: %.2f minutes", info["age_minutes"])
			log.Printf("  - Time until refresh: %v", info["time_until_refresh"])
			log.Printf("  - Total refreshes: %v", info["total_refreshes"])
			log.Printf("  - Successful: %v", info["successful_refreshes"])
			log.Printf("  - Failed: %v", info["failed_refreshes"])
			log.Printf("  - Circuit breaker: %v", info["circuit_breaker_open"])

			// Test an API call
			if _, err := sm.RMSLimit(); err != nil {
				log.Printf("[test] ⚠️  RMSLimit API call failed: %v", err)
			} else {
				log.Println("[test] ✅ RMSLimit API call successful")
			}
		}
	}
}
