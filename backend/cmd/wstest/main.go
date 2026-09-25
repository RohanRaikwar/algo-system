// cmd/wstest — Quick backend WebSocket test for Angel One SmartAPI.
//
// Connects to the Angel One WebSocket, subscribes to NIFTY 50 index
// and the dynamically resolved CE/PE F&O tokens, and prints live ticks.
//
// Usage:
//
//	set -a && source ../.env && set +a
//	go run ./cmd/wstest
//
// Market must be open (9:15–15:30 IST Mon-Fri) for ticks to flow.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	apiKey := os.Getenv("ANGEL_API_KEY")
	clientCode := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientCode == "" {
		log.Fatal("Missing ANGEL_API_KEY / ANGEL_CLIENT_CODE in env")
	}

	// ── 1. Login via REST API ──
	totpCode, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		log.Fatalf("TOTP failed: %v", err)
	}

	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: apiKey})
	session, err := sc.GenerateSession(clientCode, password, totpCode)
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}

	// Extract tokens from session
	data, ok := session["data"].(map[string]interface{})
	if !ok {
		log.Fatalf("Unexpected session response: %v", session)
	}
	jwtToken := fmt.Sprint(data["jwtToken"])
	feedToken := fmt.Sprint(data["feedToken"])
	log.Printf("✅ Logged in | clientCode=%s", clientCode)
	log.Printf("   JWT token: %s...", jwtToken[:20])
	log.Printf("   Feed token: %s...", feedToken[:20])

	// ── 2. Resolve ATM strikes dynamically ──
	picker := orderexec.NewStrikePicker(sc)
	// Use a sample spot price — if market is open, we'll get the real one from ticks
	// For now use 23350 (approximate NIFTY level)
	niftySpotPaise := int64(2335000) // ₹23,350.00 in paise
	log.Printf("Resolving ATM strikes for spot=%.2f...", float64(niftySpotPaise)/100)
	if !picker.ResolveATM(niftySpotPaise) {
		log.Println("⚠️  Failed to resolve ATM strikes — will subscribe to NIFTY index only")
	}

	ce := picker.GetCallToken()
	pe := picker.GetPutToken()

	// ── 3. Connect WebSocket ──
	ws, err := smartconnect.NewSmartWebSocketV3(
		jwtToken, apiKey, clientCode, feedToken,
		3,  // maxRetryAttempt
		0,  // retryStrategy (simple)
		5,  // retryDelaySec
		2,  // retryMultiplier
		60, // retryDurationMin
	)
	if err != nil {
		log.Fatalf("WebSocket init failed: %v", err)
	}

	tickCount := 0
	ws.OnData = func(msg map[string]interface{}) {
		tickCount++
		token := fmt.Sprint(msg["token"])
		ltp := msg["last_traded_price"]
		exType := msg["exchange_type"]

		var label string
		switch token {
		case "99926000":
			label = "NIFTY50"
		case ce.Token:
			label = fmt.Sprintf("CE %s", ce.Symbol)
		case pe.Token:
			label = fmt.Sprintf("PE %s", pe.Symbol)
		default:
			label = fmt.Sprintf("token=%s", token)
		}

		// LTP from Angel is in paise (×100)
		ltpVal := int64(0)
		switch v := ltp.(type) {
		case int64:
			ltpVal = v
		case float64:
			ltpVal = int64(v)
		}

		fmt.Printf("[tick #%d] %s | LTP=%.2f | exchange=%v\n",
			tickCount, label, float64(ltpVal)/100.0, exType)
	}

	ws.OnOpen = func() {
		log.Println("✅ WebSocket OPEN")

		// Subscribe to NIFTY 50 index (NSE_CM exchange, token 99926000)
		tokens := []smartconnect.TokenListEntry{
			{ExchangeType: smartconnect.NSE_CM, Tokens: []string{"99926000"}},
		}

		// Add F&O tokens if resolved
		if ce.Token != "" && pe.Token != "" {
			tokens = append(tokens, smartconnect.TokenListEntry{
				ExchangeType: smartconnect.NSE_FO,
				Tokens:       []string{ce.Token, pe.Token},
			})
			log.Printf("📊 Subscribing: NIFTY(99926000) + CE %s(%s) + PE %s(%s)",
				ce.Symbol, ce.Token, pe.Symbol, pe.Token)
		} else {
			log.Println("📊 Subscribing: NIFTY(99926000) only")
		}

		if err := ws.Subscribe("test_corr", smartconnect.ModeLTP, tokens); err != nil {
			log.Printf("❌ Subscribe failed: %v", err)
		}
	}

	ws.OnClose = func() {
		log.Println("WebSocket CLOSED")
	}

	ws.OnError = func(code, msg string) {
		log.Printf("❌ WebSocket error: %s — %s", code, msg)
	}

	if err := ws.Connect(); err != nil {
		log.Fatalf("❌ WebSocket connect failed: %v", err)
	}

	log.Println("╔══════════════════════════════════════════╗")
	log.Println("║  WebSocket Test Active                   ║")
	log.Println("║  Waiting for ticks... (Ctrl+C to stop)   ║")
	log.Println("╚══════════════════════════════════════════╝")

	// Print stats every 10s
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			log.Printf("📈 Stats: %d ticks received so far", tickCount)
		}
	}()

	// Wait for Ctrl+C
	_ = json.Marshal // suppress unused import
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("Shutting down... (received %d ticks total)", tickCount)
	ws.CloseConnection()
}
