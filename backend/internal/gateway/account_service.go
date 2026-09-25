package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trading-systemv1/pkg/smartconnect"
)

// AccountBalance represents the user's Angel One account balance and margin details
type AccountBalance struct {
	AvailableBalance int64  `json:"available_balance"` // in paise
	UsedMargin       int64  `json:"used_margin"`       // in paise
	TotalBalance     int64  `json:"total_balance"`     // in paise
	NetAvailable     int64  `json:"net_available"`     // in paise
	LastUpdated      string `json:"last_updated"`
}

// LastOrder represents a recent order from the signal journal
type LastOrder struct {
	ID         string `json:"id"`
	Strategy   string `json:"strategy"`
	Instrument string `json:"instrument"`
	Side       string `json:"side"`
	Action     string `json:"action"`
	Price      int64  `json:"price"` // in paise
	Qty        int    `json:"qty"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

// LastOrdersResponse wraps the list of last orders
type LastOrdersResponse struct {
	Orders []LastOrder `json:"orders"`
}

// AccountService handles account-related operations
type AccountService struct {
	db             *sql.DB
	client         *smartconnect.SmartConnect
	sessionManager *smartconnect.SessionManager
}

// NewAccountService creates a new account service instance
func NewAccountService(db *sql.DB, client *smartconnect.SmartConnect) *AccountService {
	return &AccountService{
		db:     db,
		client: client,
	}
}

// NewAccountServiceWithSessionManager creates a new account service with session manager
func NewAccountServiceWithSessionManager(db *sql.DB, sessionManager *smartconnect.SessionManager) *AccountService {
	return &AccountService{
		db:             db,
		client:         sessionManager.GetClient(),
		sessionManager: sessionManager,
	}
}

// GetAccountBalance fetches current account balance and margin from Angel One
func (s *AccountService) GetAccountBalance(ctx context.Context) (AccountBalance, error) {
	if s.client == nil {
		return AccountBalance{}, fmt.Errorf("smartconnect client not initialized")
	}

	// Use session manager if available (handles auto-refresh on token expiry)
	var res map[string]any
	var err error

	if s.sessionManager != nil {
		res, err = s.sessionManager.RMSLimit()
	} else {
		res, err = s.client.RMSLimit()
	}

	// Handle rate limiting with retry logic
	if err != nil && (strings.Contains(err.Error(), "exceeding access rate") ||
		strings.Contains(err.Error(), "Access denied") ||
		strings.Contains(err.Error(), "rate limit")) {

		maxRetries := 3
		retryDelay := time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			log.Printf("[account] ⚠️  rate limited by Angel One (attempt %d/%d), retrying in %v...", attempt, maxRetries, retryDelay)
			time.Sleep(retryDelay)

			if s.sessionManager != nil {
				res, err = s.sessionManager.RMSLimit()
			} else {
				res, err = s.client.RMSLimit()
			}

			if err == nil {
				break
			}
			retryDelay *= 2 // Exponential backoff
		}

		if err != nil {
			log.Printf("[account] ⚠️  rate limit exceeded after %d attempts", maxRetries)
			return AccountBalance{}, fmt.Errorf("Angel One API rate limit exceeded, please try again in a few seconds")
		}
	}

	if err != nil {
		log.Printf("[account] ⚠️  failed to fetch RMS limit: %v", err)
		return AccountBalance{}, fmt.Errorf("failed to fetch account balance: %w", err)
	}

	// Parse the response
	// Expected structure: {"status": true, "data": {...}}
	status, _ := res["status"].(bool)
	if !status {
		msg, _ := res["message"].(string)
		return AccountBalance{}, fmt.Errorf("RMS API returned error: %s", msg)
	}

	data, ok := res["data"].(map[string]any)
	if !ok {
		return AccountBalance{}, fmt.Errorf("unexpected RMS response format")
	}

	// Extract margin details
	// Angel One returns values as strings, need to parse them
	netStr, _ := data["net"].(string)
	availablecashStr, _ := data["availablecash"].(string)
	usedmarginStr, _ := data["m2munrealized"].(string) // or "utiliseddebits"

	// Parse string values to float and convert to paise
	net := parseFloatToPaise(netStr)
	availableCash := parseFloatToPaise(availablecashStr)
	usedMargin := parseFloatToPaise(usedmarginStr)

	// Calculate total balance (net + used margin)
	totalBalance := net + usedMargin

	return AccountBalance{
		AvailableBalance: availableCash,
		UsedMargin:       usedMargin,
		TotalBalance:     totalBalance,
		NetAvailable:     net,
		LastUpdated:      time.Now().Format(time.RFC3339),
	}, nil
}

// GetLastOrders fetches last N orders from Angel One order book
func (s *AccountService) GetLastOrders(ctx context.Context, limit int) (LastOrdersResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	if s.client == nil {
		return LastOrdersResponse{}, fmt.Errorf("smartconnect client not initialized")
	}

	// Use session manager if available (handles auto-refresh on token expiry)
	var res map[string]interface{}
	var err error

	if s.sessionManager != nil {
		res, err = s.sessionManager.OrderBook()
	} else {
		res, err = s.client.OrderBook()
	}

	// Handle rate limiting with retry logic
	if err != nil && (strings.Contains(err.Error(), "exceeding access rate") ||
		strings.Contains(err.Error(), "Access denied") ||
		strings.Contains(err.Error(), "rate limit")) {

		maxRetries := 3
		retryDelay := time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			log.Printf("[account] ⚠️  rate limited by Angel One (attempt %d/%d), retrying in %v...", attempt, maxRetries, retryDelay)
			time.Sleep(retryDelay)

			if s.sessionManager != nil {
				res, err = s.sessionManager.OrderBook()
			} else {
				res, err = s.client.OrderBook()
			}

			if err == nil {
				break
			}
			retryDelay *= 2 // Exponential backoff
		}

		if err != nil {
			log.Printf("[account] ⚠️  rate limit exceeded after %d attempts", maxRetries)
			return LastOrdersResponse{}, fmt.Errorf("Angel One API rate limit exceeded, please try again in a few seconds")
		}
	}

	if err != nil {
		log.Printf("[account] ⚠️  failed to fetch order book from Angel One: %v", err)
		return LastOrdersResponse{}, fmt.Errorf("failed to fetch order book: %w", err)
	}

	// Parse the response
	status, _ := res["status"].(bool)
	if !status {
		msg, _ := res["message"].(string)
		return LastOrdersResponse{}, fmt.Errorf("order book API returned error: %s", msg)
	}

	// Log the response structure for debugging
	log.Printf("[account] order book response: %+v", res)

	data, ok := res["data"].([]interface{})
	if !ok {
		// Try alternative format - might be a map or nil
		if res["data"] == nil {
			log.Printf("[account] order book data is nil - no orders found")
			return LastOrdersResponse{Orders: []LastOrder{}}, nil
		}
		log.Printf("[account] unexpected data type: %T, value: %+v", res["data"], res["data"])
		return LastOrdersResponse{}, fmt.Errorf("unexpected order book response format")
	}

	// Convert Angel One orders to our format
	orders := make([]LastOrder, 0, limit)
	for i, item := range data {
		if i >= limit {
			break
		}

		orderMap, ok := item.(map[string]interface{})
		if !ok {
			log.Printf("[account] ⚠️  skipping non-map order item at index %d: %T", i, item)
			continue
		}

		// Extract order details from Angel One response with safe type assertions
		orderID := safeString(orderMap["orderid"])
		tradingSymbol := safeString(orderMap["tradingsymbol"])
		transactionType := safeString(orderMap["transactiontype"]) // BUY or SELL
		orderStatus := safeString(orderMap["orderstatus"])
		priceStr := safeString(orderMap["price"])
		qtyStr := safeString(orderMap["quantity"])
		updateTime := safeString(orderMap["updatetime"])
		productType := safeString(orderMap["producttype"])

		// Parse price (in rupees, convert to paise)
		price := int64(0)
		if priceStr != "" && priceStr != "0" {
			if f, err := strconv.ParseFloat(priceStr, 64); err == nil {
				price = int64(f * 100) // Convert to paise
			}
		}

		// Parse quantity
		qty := 1
		if qtyStr != "" {
			if q, err := strconv.Atoi(qtyStr); err == nil && q > 0 {
				qty = q
			}
		}

		// Map transaction type to action
		action := transactionType // BUY or SELL

		// Determine side from trading symbol (CE for CALL, PE for PUT)
		side := ""
		if strings.Contains(tradingSymbol, "CE") {
			side = "CALL"
		} else if strings.Contains(tradingSymbol, "PE") {
			side = "PUT"
		}

		orders = append(orders, LastOrder{
			ID:         orderID,
			Strategy:   productType, // Use product type as strategy indicator
			Instrument: tradingSymbol,
			Side:       side,
			Action:     action,
			Price:      price,
			Qty:        qty,
			Status:     orderStatus,
			CreatedAt:  updateTime,
		})
	}

	return LastOrdersResponse{Orders: orders}, nil
}

// safeString safely extracts a string from an interface{}, handling various types
func safeString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// GetUserProfile fetches user profile information from Angel One
func (s *AccountService) GetUserProfile(ctx context.Context) (map[string]any, error) {
	if s.client == nil {
		return nil, fmt.Errorf("smartconnect client not initialized")
	}

	// Use session manager if available (handles auto-refresh on token expiry)
	var res map[string]any
	var err error

	if s.sessionManager != nil {
		res, err = s.sessionManager.GetProfile("")
	} else {
		res, err = s.client.GetProfile("")
	}

	// Handle rate limiting with retry logic
	if err != nil && (strings.Contains(err.Error(), "exceeding access rate") ||
		strings.Contains(err.Error(), "Access denied") ||
		strings.Contains(err.Error(), "rate limit")) {

		maxRetries := 3
		retryDelay := time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			log.Printf("[account] ⚠️  rate limited by Angel One (attempt %d/%d), retrying in %v...", attempt, maxRetries, retryDelay)
			time.Sleep(retryDelay)

			if s.sessionManager != nil {
				res, err = s.sessionManager.GetProfile("")
			} else {
				res, err = s.client.GetProfile("")
			}

			if err == nil {
				break
			}
			retryDelay *= 2 // Exponential backoff
		}

		if err != nil {
			log.Printf("[account] ⚠️  rate limit exceeded after %d attempts", maxRetries)
			return nil, fmt.Errorf("Angel One API rate limit exceeded, please try again in a few seconds")
		}
	}

	if err != nil {
		log.Printf("[account] ⚠️  failed to fetch user profile: %v", err)
		return nil, fmt.Errorf("failed to fetch user profile: %w", err)
	}

	return res, nil
}

// parseFloatToPaise converts a decimal rupee string ("1234.29", "-0.5")
// to int64 paise without a float intermediate, which would truncate
// 0.29 to 28 paise. Digits past the second decimal are rounded half away
// from zero.
func parseFloatToPaise(s string) int64 {
	p, err := rupeesToPaise(s)
	if err != nil {
		log.Printf("[account] ⚠️  failed to parse amount '%s': %v", s, err)
		return 0
	}
	return p
}

func rupeesToPaise(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" {
		return 0, fmt.Errorf("no digits")
	}
	for _, part := range []string{whole, frac} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("invalid character %q", c)
			}
		}
	}
	if whole == "" {
		whole = "0"
	}
	r, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	frac += "000"
	paise, _ := strconv.ParseInt(frac[:2], 10, 64)
	total := r*100 + paise
	if frac[2] >= '5' {
		total++
	}
	if neg {
		total = -total
	}
	return total, nil
}

// HTTP Handlers

// makeAccountBalanceHandler creates HTTP handler for account balance endpoint
func makeAccountBalanceHandler(svc *AccountService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		SetCORS(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		balance, err := svc.GetAccountBalance(r.Context())
		if err != nil {
			log.Printf("[account] ⚠️  balance fetch error: %v", err)
			http.Error(w, "Failed to fetch account balance", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(balance); err != nil {
			log.Printf("[account] ⚠️  failed to encode balance response: %v", err)
		}
	}
}

// makeLastOrdersHandler creates HTTP handler for last orders endpoint
func makeLastOrdersHandler(svc *AccountService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		SetCORS(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		limitStr := r.URL.Query().Get("limit")
		limit := 10
		if limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
				limit = parsed
			}
		}

		orders, err := svc.GetLastOrders(r.Context(), limit)
		if err != nil {
			log.Printf("[account] ⚠️  orders fetch error: %v", err)
			http.Error(w, "Failed to fetch last orders", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(orders); err != nil {
			log.Printf("[account] ⚠️  failed to encode orders response: %v", err)
		}
	}
}

// makeUserProfileHandler creates HTTP handler for user profile endpoint
func makeUserProfileHandler(svc *AccountService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		SetCORS(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		profile, err := svc.GetUserProfile(r.Context())
		if err != nil {
			log.Printf("[account] ⚠️  profile fetch error: %v", err)
			http.Error(w, "Failed to fetch user profile", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(profile); err != nil {
			log.Printf("[account] ⚠️  failed to encode profile response: %v", err)
		}
	}
}

// makeSessionHealthHandler creates HTTP handler for session health endpoint
func makeSessionHealthHandler(svc *AccountService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		SetCORS(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if svc.sessionManager == nil {
			http.Error(w, "Session manager not available", http.StatusServiceUnavailable)
			return
		}

		sessionInfo := svc.sessionManager.GetSessionInfo()
		metrics := svc.sessionManager.GetMetrics()
		isHealthy := svc.sessionManager.IsHealthy()

		response := map[string]any{
			"healthy":      isHealthy,
			"session_info": sessionInfo,
			"metrics": map[string]any{
				"total_refreshes":       metrics.TotalRefreshes,
				"successful_refreshes":  metrics.SuccessfulRefreshes,
				"failed_refreshes":      metrics.FailedRefreshes,
				"last_refresh_time":     metrics.LastRefreshTime.Format(time.RFC3339),
				"last_refresh_duration": metrics.LastRefreshDuration.String(),
				"session_age":           metrics.SessionAge.String(),
				"session_age_minutes":   metrics.SessionAge.Minutes(),
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("[account] ⚠️  failed to encode session health response: %v", err)
		}
	}
}
