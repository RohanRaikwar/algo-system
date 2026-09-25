// cmd/tickserver — Demo WebSocket tick server.
// Broadcasts simulated tick data for testing mdengine-sim without real broker credentials.
//
// Tick JSON shape is identical to model.Tick:
//
//	{"token":"2885","exchange":"NSE","price":185005000,"qty":10,"tick_ts":"..."}
//
// Price is stored in paise (1 INR = 100 paise), same as live feed.
//
// Config (env vars):
//
//	TICK_SERVER_ADDR  — listen address  (default: ":9001")
//	TICK_TOKENS       — comma-separated TOKEN:EXCHANGE pairs (default: "99926000:NSE")
//	TICK_INTERVAL_MS  — broadcast interval milliseconds (default: "100")
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// tickMsg mirrors model.Tick for JSON serialisation.
type tickMsg struct {
	Token    string    `json:"token"`
	Exchange string    `json:"exchange"`
	Price    int64     `json:"price"` // paise
	Qty      int64     `json:"qty"`
	TickTS   time.Time `json:"tick_ts"`
}

// instrument holds per-symbol simulation state.
type instrument struct {
	Token        string
	Exchange     string
	Price        int64 // current simulated price in paise
	BasePrice    int64 // anchor price for mean-reversion
	ScenarioMode bool  // if true, use scripted price phases instead of random walk
}

// ─── Hub ──────────────────────────────────────────────────────────────────────

type hub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]chan []byte
}

func newHub() *hub {
	return &hub{clients: make(map[*websocket.Conn]chan []byte)}
}

func (h *hub) register(conn *websocket.Conn) chan []byte {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.clients[conn] = ch
	h.mu.Unlock()
	return ch
}

func (h *hub) unregister(conn *websocket.Conn) {
	h.mu.Lock()
	if ch, ok := h.clients[conn]; ok {
		close(ch)
		delete(h.clients, conn)
	}
	h.mu.Unlock()
}

func (h *hub) broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.clients {
		select {
		case ch <- msg:
		default: // slow client — drop tick
		}
	}
}

// ─── WebSocket handler ────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func wsHandler(h *hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[tickserver] upgrade error: %v", err)
			return
		}
		log.Printf("[tickserver] client connected: %s", r.RemoteAddr)

		ch := h.register(conn)
		defer func() {
			h.unregister(conn)
			conn.Close()
			log.Printf("[tickserver] client disconnected: %s", r.RemoteAddr)
		}()

		// Write pump: sends tick JSON to this client.
		for msg := range ch {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

// ─── Tick generator ──────────────────────────────────────────────────────────

// walkPrice applies a tiny random walk with strong mean-reversion to simulate
// realistic index price movement. Keeps prices within a tight band around the
// base price, producing clean indicator overlay with minimal lag.
func walkPrice(price, basePrice int64) int64 {
	// Random component: ±0.005% per tick (very small noise)
	pct := (rand.Float64()*0.01 - 0.005) / 100.0

	// Strong mean-reversion: pull 0.1% of deviation back toward base
	deviation := float64(price-basePrice) / float64(basePrice)
	reversion := -deviation * 0.001

	totalPct := pct + reversion
	delta := int64(float64(price) * totalPct)
	newPrice := price + delta
	if newPrice < 100 {
		newPrice = 100
	}
	return newPrice
}

// scenarioPrice returns a deterministic price based on elapsed time since startup.
// Drives the NIFTY index through 4 phases to trigger all signal types:
//
//	Phase 1 (0–25 min):  Warm-up — flat at base price, lets EMAs converge
//	Phase 2 (25–30 min): Bull Rally — ramp +₹200, triggers BUY CALL
//	Phase 3 (30–35 min): Bear Drop — drop -₹400 from peak, triggers EXIT CALL + BUY PUT
//	Phase 4 (35–40 min): Recovery — ramp +₹300, triggers EXIT PUT
//	After 40 min:        Settles at new level, reverts to random walk
func scenarioPrice(basePrice int64, elapsed time.Duration) (int64, bool) {
	mins := elapsed.Minutes()
	switch {
	case mins < 25: // Phase 1: Warm-up — flat
		// Add tiny noise so candles have distinct OHLC
		noise := int64((rand.Float64() - 0.5) * 20_00) // ±₹10 noise
		return basePrice + noise, true
	case mins < 30: // Phase 2: Bull rally — ramp up +₹200
		progress := (mins - 25) / 5.0
		ramp := int64(progress * 200_00)
		noise := int64((rand.Float64() - 0.5) * 10_00)
		return basePrice + ramp + noise, true
	case mins < 35: // Phase 3: Bear drop — drop -₹400 from peak
		progress := (mins - 30) / 5.0
		peak := int64(200_00)
		drop := int64(progress * 400_00)
		noise := int64((rand.Float64() - 0.5) * 10_00)
		return basePrice + peak - drop + noise, true
	case mins < 40: // Phase 4: Recovery — ramp up +₹300
		progress := (mins - 35) / 5.0
		base := int64(-200_00) // bottom after bear drop
		ramp := int64(progress * 300_00)
		noise := int64((rand.Float64() - 0.5) * 10_00)
		return basePrice + base + ramp + noise, true
	default: // Scenario complete — revert to random walk
		return basePrice + 100_00, false
	}
}

func runGenerator(h *hub, instruments []instrument, intervalMs int, startTime time.Time) {
	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	_ = rng

	// Track which instruments have completed their scenario
	scenarioDone := make(map[int]bool)

	for range ticker.C {
		elapsed := time.Since(startTime)
		for i := range instruments {
			if instruments[i].ScenarioMode && !scenarioDone[i] {
				// Use scripted price scenario
				price, active := scenarioPrice(instruments[i].BasePrice, elapsed)
				if active {
					instruments[i].Price = price
				} else {
					// Scenario complete — switch to random walk with new base
					instruments[i].BasePrice = price
					instruments[i].Price = price
					instruments[i].ScenarioMode = false
					scenarioDone[i] = true
					log.Printf("[tickserver] ✅ scenario complete for %s:%s — reverting to random walk at price %d",
						instruments[i].Exchange, instruments[i].Token, price)
				}
			} else {
				instruments[i].Price = walkPrice(instruments[i].Price, instruments[i].BasePrice)
			}

			msg := tickMsg{
				Token:    instruments[i].Token,
				Exchange: instruments[i].Exchange,
				Price:    instruments[i].Price,
				Qty:      int64(rand.Intn(100) + 1),
				TickTS:   time.Now().UTC(),
			}
			b, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			h.broadcast(b)
		}
	}
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[tickserver] starting demo tick server...")

	// Config
	addr := envOrDefault("TICK_SERVER_ADDR", ":9001")
	tokensEnv := envOrDefault("TICK_TOKENS", "99926000:NSE")
	intervalMs := envIntOrDefault("TICK_INTERVAL_MS", 100)
	scenarioEnabled := envOrDefault("TICK_SCENARIO", "false") == "true"

	// Parse TOKEN:EXCHANGE pairs
	instruments := parseInstruments(tokensEnv)
	if len(instruments) == 0 {
		log.Fatalf("[tickserver] no instruments configured via TICK_TOKENS")
	}

	// Enable scenario mode for the index token (99926000)
	if scenarioEnabled {
		for i := range instruments {
			if instruments[i].Token == "99926000" {
				instruments[i].ScenarioMode = true
				log.Printf("[tickserver] 🎬 SCENARIO MODE enabled for %s:%s",
					instruments[i].Exchange, instruments[i].Token)
				log.Println("[tickserver]   Phase 1 (0-25m):  Warm-up at base price")
				log.Println("[tickserver]   Phase 2 (25-30m): Bull rally +₹200 → BUY CALL")
				log.Println("[tickserver]   Phase 3 (30-35m): Bear drop -₹400 → EXIT CALL + BUY PUT")
				log.Println("[tickserver]   Phase 4 (35-40m): Recovery +₹300 → EXIT PUT")
			}
		}
	}

	log.Printf("[tickserver] instruments: %+v", instruments)
	log.Printf("[tickserver] broadcast interval: %dms", intervalMs)

	startTime := time.Now()
	h := newHub()

	// Start tick generator
	go runGenerator(h, instruments, intervalMs, startTime)

	// HTTP routes
	http.HandleFunc("/ws", wsHandler(h))
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"status":"ok","service":"tickserver"}`)
	})

	log.Printf("[tickserver] ✅ listening on %s  (WebSocket: ws://localhost%s/ws)", addr, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("[tickserver] server error: %v", err)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseInstruments(s string) []instrument {
	// Default starting prices in paise (INR × 100)
	defaultPrices := map[string]int64{
		"2885":     185050_00, // ~₹18505.00 (Reliance)
		"1594":     250000_00, // ~₹25000.00
		"99926009": 25660_00,  // ~₹25660.00 (NIFTY sim alt)
		"99926000": 25660_00,  // ~₹25660.00 (NIFTY 50 index sim)
		"45497":    25000,     // ~₹250.00 (NIFTY CALL option)
		"45498":    18000,     // ~₹180.00 (NIFTY PUT option)
	}

	var result []instrument
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		seg := strings.SplitN(part, ":", 2)
		if len(seg) != 2 {
			log.Printf("[tickserver] skipping invalid token spec: %q", part)
			continue
		}
		token, exchange := strings.TrimSpace(seg[0]), strings.TrimSpace(seg[1])
		price := defaultPrices[token]
		if price == 0 {
			price = 100000_00 // default ₹1000.00
		}
		result = append(result, instrument{
			Token:     token,
			Exchange:  exchange,
			Price:     price,
			BasePrice: price,
		})
	}
	return result
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
