package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"trading-systemv1/pkg/smartconnect"

	"github.com/joho/godotenv"
	"github.com/pquerna/otp/totp"
)

func main() {
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

	log.Printf("✅ Logged in successfully\n")

	// Get order book
	log.Println("📋 Fetching order book...")
	result, err := sc.OrderBook()
	if err != nil {
		log.Fatalf("❌ Failed to fetch orders: %v", err)
	}

	// Pretty print
	jsonData, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(jsonData))

	// Parse and display orders
	if data, ok := result["data"].([]interface{}); ok {
		fmt.Println("\n╔════════════════════════════════════════════════════════════════════════════╗")
		fmt.Println("║                              ORDER BOOK                                    ║")
		fmt.Println("╚════════════════════════════════════════════════════════════════════════════╝")
		
		openOrders := 0
		for i, item := range data {
			if orderMap, ok := item.(map[string]interface{}); ok {
				orderID := getString(orderMap, "orderid")
				symbol := getString(orderMap, "tradingsymbol")
				status := getString(orderMap, "orderstatus")
				txnType := getString(orderMap, "transactiontype")
				qty := getString(orderMap, "quantity")
				price := getString(orderMap, "price")
				productType := getString(orderMap, "producttype")
				
				fmt.Printf("\n%d. Order ID: %s\n", i+1, orderID)
				fmt.Printf("   Symbol: %s\n", symbol)
				fmt.Printf("   Status: %s\n", status)
				fmt.Printf("   Type: %s\n", txnType)
				fmt.Printf("   Qty: %s\n", qty)
				fmt.Printf("   Price: ₹%s\n", price)
				fmt.Printf("   Product: %s\n", productType)
				
				if status == "open" || status == "pending" || status == "trigger pending" {
					openOrders++
					fmt.Printf("   ⚠️  OPEN ORDER - Can be cancelled\n")
					fmt.Printf("   Cancel command: go run backend/cmd/cancelorder/main.go %s\n", orderID)
				}
			}
		}
		
		fmt.Printf("\n📊 Total orders: %d\n", len(data))
		fmt.Printf("⚠️  Open orders: %d\n", openOrders)
		
		if openOrders > 0 {
			fmt.Println("\n💡 TIP: Cancel open orders using:")
			fmt.Println("   go run backend/cmd/cancelorder/main.go <ORDER_ID>")
		}
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}
