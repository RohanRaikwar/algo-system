package smartconnect

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
)

// SessionMetrics tracks session refresh statistics
type SessionMetrics struct {
	TotalRefreshes      int64
	SuccessfulRefreshes int64
	FailedRefreshes     int64
	LastRefreshTime     time.Time
	LastRefreshDuration time.Duration
	SessionAge          time.Duration
}

// SessionManager wraps SmartConnect and handles automatic session refresh
type SessionManager struct {
	client       *SmartConnect
	clientID     string
	password     string
	totpSecret   string
	mu           sync.RWMutex
	lastRefresh  time.Time
	sessionValid bool

	// Proactive refresh settings
	sessionTTL         time.Duration // Expected session lifetime (default: 90 minutes)
	proactiveThreshold time.Duration // Refresh before expiry (default: 15 minutes before)

	// Circuit breaker for repeated failures
	consecutiveFailures int
	maxFailures         int       // Max consecutive failures before circuit opens
	circuitOpen         bool      // Circuit breaker state
	circuitOpenTime     time.Time // When circuit was opened
	circuitResetTimeout time.Duration

	// Metrics
	metrics SessionMetrics

	// Background refresh
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSessionManager creates a new session manager with auto-refresh capability
func NewSessionManager(apiKey, clientID, password, totpSecret string, debug bool) *SessionManager {
	client := NewSmartConnect(Config{
		APIKey: apiKey,
		Debug:  debug,
	})

	ctx, cancel := context.WithCancel(context.Background())

	sm := &SessionManager{
		client:              client,
		clientID:            clientID,
		password:            password,
		totpSecret:          totpSecret,
		sessionValid:        false,
		sessionTTL:          30 * time.Minute, // Angel One sessions expire in ~30 minutes
		proactiveThreshold:  10 * time.Minute, // Refresh 10 minutes before expiry (at 20 min mark)
		maxFailures:         3,
		circuitResetTimeout: 5 * time.Minute,
		ctx:                 ctx,
		cancel:              cancel,
	}

	return sm
}

// Start begins background proactive session refresh
func (sm *SessionManager) Start() {
	log.Printf("[session_manager] 🚀 starting proactive refresh loop (check every 1 min, refresh at %v)", 
		sm.sessionTTL-sm.proactiveThreshold)
	go sm.proactiveRefreshLoop()
}

// Stop stops the background refresh loop
func (sm *SessionManager) Stop() {
	if sm.cancel != nil {
		sm.cancel()
	}
}

// Login establishes initial session with Angel One
func (sm *SessionManager) Login() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Println("[session_manager] 🔑 logging in to Angel One...")
	startTime := time.Now()

	totpCode, err := totp.GenerateCode(sm.totpSecret, time.Now())
	if err != nil {
		sm.recordFailure()
		return fmt.Errorf("TOTP generation failed: %w", err)
	}

	_, err = sm.client.GenerateSession(sm.clientID, sm.password, totpCode)
	if err != nil {
		sm.sessionValid = false
		sm.recordFailure()
		return fmt.Errorf("login failed: %w", err)
	}

	sm.lastRefresh = time.Now()
	sm.sessionValid = true
	sm.consecutiveFailures = 0
	sm.circuitOpen = false
	
	// Update metrics
	sm.metrics.TotalRefreshes++
	sm.metrics.SuccessfulRefreshes++
	sm.metrics.LastRefreshTime = sm.lastRefresh
	sm.metrics.LastRefreshDuration = time.Since(startTime)
	
	log.Printf("[session_manager] ✅ session established at %s (took %v)", 
		sm.lastRefresh.Format("15:04:05"), sm.metrics.LastRefreshDuration)

	return nil
}

// recordFailure tracks consecutive failures for circuit breaker
func (sm *SessionManager) recordFailure() {
	sm.consecutiveFailures++
	sm.metrics.TotalRefreshes++
	sm.metrics.FailedRefreshes++
	
	if sm.consecutiveFailures >= sm.maxFailures {
		sm.circuitOpen = true
		sm.circuitOpenTime = time.Now()
		log.Printf("[session_manager] 🔴 circuit breaker OPEN after %d consecutive failures", sm.consecutiveFailures)
	}
}

// RefreshSession creates a new session when the current one expires
func (sm *SessionManager) RefreshSession() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check circuit breaker
	if sm.circuitOpen {
		if time.Since(sm.circuitOpenTime) < sm.circuitResetTimeout {
			return fmt.Errorf("circuit breaker open, retry after %v", 
				sm.circuitResetTimeout-time.Since(sm.circuitOpenTime))
		}
		// Reset circuit breaker after timeout
		log.Println("[session_manager] 🟡 circuit breaker reset, attempting refresh...")
		sm.circuitOpen = false
		sm.consecutiveFailures = 0
	}

	log.Println("[session_manager] 🔄 refreshing expired session...")
	startTime := time.Now()

	totpCode, err := totp.GenerateCode(sm.totpSecret, time.Now())
	if err != nil {
		sm.recordFailure()
		return fmt.Errorf("TOTP generation failed: %w", err)
	}

	_, err = sm.client.GenerateSession(sm.clientID, sm.password, totpCode)
	if err != nil {
		sm.sessionValid = false
		sm.recordFailure()
		return fmt.Errorf("session refresh failed: %w", err)
	}

	sm.lastRefresh = time.Now()
	sm.sessionValid = true
	sm.consecutiveFailures = 0
	
	// Update metrics
	sm.metrics.TotalRefreshes++
	sm.metrics.SuccessfulRefreshes++
	sm.metrics.LastRefreshTime = sm.lastRefresh
	sm.metrics.LastRefreshDuration = time.Since(startTime)
	
	log.Printf("[session_manager] ✅ session refreshed at %s (took %v)", 
		sm.lastRefresh.Format("15:04:05"), sm.metrics.LastRefreshDuration)

	return nil
}

// proactiveRefreshLoop runs in background and refreshes session before expiry
func (sm *SessionManager) proactiveRefreshLoop() {
	ticker := time.NewTicker(1 * time.Minute) // Check every minute
	defer ticker.Stop()
	
	log.Println("[session_manager] 🔄 proactive refresh loop started")

	for {
		select {
		case <-sm.ctx.Done():
			log.Println("[session_manager] 🛑 stopping proactive refresh loop")
			return
		case <-ticker.C:
			sm.checkAndRefreshIfNeeded()
		}
	}
}

// checkAndRefreshIfNeeded checks session age and refreshes proactively.
// Skips when no Login() has succeeded yet — otherwise the zero-value
// lastRefresh produces a session age in the billions of years and triggers
// a refresh storm before the operator's initial Login attempt.
func (sm *SessionManager) checkAndRefreshIfNeeded() {
	sm.mu.RLock()
	if sm.lastRefresh.IsZero() {
		sm.mu.RUnlock()
		return
	}
	sessionAge := time.Since(sm.lastRefresh)
	refreshThreshold := sm.sessionTTL - sm.proactiveThreshold
	valid := sm.sessionValid
	sm.mu.RUnlock()

	shouldRefresh := !valid || sessionAge >= refreshThreshold

	// Log every 2 minutes for debugging (since sessions are short)
	// Or log whenever we are disconnected to spam visibility
	if !valid || int(sessionAge.Minutes())%2 == 0 {
		log.Printf("[session_manager] 🕐 session check: age=%v, threshold=%v, valid=%v, should_refresh=%v",
			sessionAge.Round(time.Second), refreshThreshold, valid, shouldRefresh)
	}

	if shouldRefresh {
		if !valid {
			log.Printf("[session_manager] ⏰ proactive refresh triggered (session is INVALID)")
		} else {
			log.Printf("[session_manager] ⏰ proactive refresh triggered (session age: %v, threshold: %v)",
				sessionAge.Round(time.Second), refreshThreshold)
		}

		if err := sm.RefreshSession(); err != nil {
			log.Printf("[session_manager] ⚠️  proactive refresh failed: %v", err)
		} else {
			log.Printf("[session_manager] ✅ proactive refresh successful")
		}
	}
}

// isSessionExpiredError checks if the error indicates an expired session
func isSessionExpiredError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "Invalid Token") ||
		strings.Contains(errStr, "AB1007") ||
		strings.Contains(errStr, "TokenException") ||
		strings.Contains(errStr, "Session expired")
}

// executeWithRetry executes a function and retries once with session refresh
// if it fails due to expired session.
//
// Gates the broker call on the circuit breaker state so that during an
// outage we do not keep hammering Angel One with raw API calls only to
// have RefreshSession reject the retry. This makes the breaker an
// invariant for every broker-bound call routed through SessionManager.
func (sm *SessionManager) executeWithRetry(fn func() (map[string]any, error)) (map[string]any, error) {
	sm.mu.RLock()
	circuitOpen := sm.circuitOpen
	circuitOpenedAt := sm.circuitOpenTime
	resetTimeout := sm.circuitResetTimeout
	sm.mu.RUnlock()
	if circuitOpen && time.Since(circuitOpenedAt) < resetTimeout {
		return nil, fmt.Errorf("session manager circuit breaker open, retry after %v",
			resetTimeout-time.Since(circuitOpenedAt))
	}

	// First attempt
	result, err := fn()

	// If no error or not a session error, return immediately
	if err == nil || !isSessionExpiredError(err) {
		return result, err
	}

	// Session expired - refresh and retry
	log.Printf("[session_manager] ⚠️  detected expired session: %v", err)

	if refreshErr := sm.RefreshSession(); refreshErr != nil {
		return nil, fmt.Errorf("session refresh failed: %w (original error: %v)", refreshErr, err)
	}

	// Retry the operation with new session
	log.Println("[session_manager] 🔄 retrying operation with new session...")
	result, err = fn()
	if err != nil {
		return nil, fmt.Errorf("operation failed after session refresh: %w", err)
	}

	return result, nil
}

// GetClient returns the underlying SmartConnect client
// Use this for operations that don't need auto-retry (like PlaceOrder)
func (sm *SessionManager) GetClient() *SmartConnect {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.client
}

// RMSLimit fetches account balance with automatic session refresh on expiry
func (sm *SessionManager) RMSLimit() (map[string]any, error) {
	return sm.executeWithRetry(func() (map[string]any, error) {
		return sm.client.RMSLimit()
	})
}

// OrderBook fetches order book with automatic session refresh on expiry
func (sm *SessionManager) OrderBook() (map[string]any, error) {
	return sm.executeWithRetry(func() (map[string]any, error) {
		return sm.client.OrderBook()
	})
}

// GetProfile fetches user profile with automatic session refresh on expiry
func (sm *SessionManager) GetProfile(refreshToken string) (map[string]any, error) {
	return sm.executeWithRetry(func() (map[string]any, error) {
		return sm.client.GetProfile(refreshToken)
	})
}

// Position fetches positions with automatic session refresh on expiry
func (sm *SessionManager) Position() (map[string]any, error) {
	return sm.executeWithRetry(func() (map[string]any, error) {
		return sm.client.Position()
	})
}

// TradeBook fetches trade book with automatic session refresh on expiry
func (sm *SessionManager) TradeBook() (map[string]any, error) {
	return sm.executeWithRetry(func() (map[string]any, error) {
		return sm.client.TradeBook()
	})
}

// Holding fetches holdings with automatic session refresh on expiry
func (sm *SessionManager) Holding() (map[string]any, error) {
	return sm.executeWithRetry(func() (map[string]any, error) {
		return sm.client.Holding()
	})
}

// GetSessionInfo returns session status information
func (sm *SessionManager) GetSessionInfo() map[string]any {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessionAge := time.Since(sm.lastRefresh)
	timeUntilRefresh := (sm.sessionTTL - sm.proactiveThreshold) - sessionAge
	if timeUntilRefresh < 0 {
		timeUntilRefresh = 0
	}

	return map[string]any{
		"valid":                  sm.sessionValid,
		"last_refresh":           sm.lastRefresh.Format(time.RFC3339),
		"age_seconds":            sessionAge.Seconds(),
		"age_minutes":            sessionAge.Minutes(),
		"time_until_refresh":     timeUntilRefresh.String(),
		"circuit_breaker_open":   sm.circuitOpen,
		"consecutive_failures":   sm.consecutiveFailures,
		"total_refreshes":        sm.metrics.TotalRefreshes,
		"successful_refreshes":   sm.metrics.SuccessfulRefreshes,
		"failed_refreshes":       sm.metrics.FailedRefreshes,
		"last_refresh_duration":  sm.metrics.LastRefreshDuration.String(),
		"session_ttl_minutes":    sm.sessionTTL.Minutes(),
		"proactive_threshold_minutes": sm.proactiveThreshold.Minutes(),
	}
}

// GetMetrics returns session metrics
func (sm *SessionManager) GetMetrics() SessionMetrics {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	metrics := sm.metrics
	metrics.SessionAge = time.Since(sm.lastRefresh)
	return metrics
}

// IsHealthy returns true if session is valid and circuit breaker is closed
func (sm *SessionManager) IsHealthy() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessionValid && !sm.circuitOpen
}
