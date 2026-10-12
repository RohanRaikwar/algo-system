package oms

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Redis streams between stratengine and the OMS process. Streams, not
// pub/sub: an intent published while the OMS is down must still be there
// when it comes back, and consumer groups track what has been handled.
const (
	IntentStream = "stream:oms:intents"
	EventStream  = "stream:oms:events"
	// IntentStreamMaxLen and EventStreamMaxLen are approximate caps
	// (XADD MAXLEN ~). ≈200 B per entry: ~2 MB and ~20 MB.
	IntentStreamMaxLen = 10000
	EventStreamMaxLen  = 100000
)

// Intent is one strategy order request. Payload is the strategy signal as
// JSON: the oms package stays free of strategy types, and the consumer
// decodes it.
type Intent struct {
	ID        string          `json:"id"`
	Strategy  string          `json:"strategy"`
	Action    string          `json:"action"`
	Side      string          `json:"side"`
	Payload   json.RawMessage `json:"payload"`
	CreatedTS time.Time       `json:"created_ts"`
}

// IntentID identifies an intent. With a market timestamp (the candle or
// tick that produced the signal) it is deterministic, so a redelivered or
// replayed signal maps to the same id; without one it is random.
func IntentID(strategy, side, action, token string, signalTS time.Time) string {
	if signalTS.IsZero() {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return fmt.Sprintf("%x", time.Now().UnixNano())
		}
		return hex.EncodeToString(b[:])
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d", strategy, side, action, token, signalTS.UnixNano())))
	return hex.EncodeToString(h[:8])
}

// Stale reports whether an entry intent is too old to act on. Exits are
// never stale: an open position must always be closable.
func (in Intent) Stale(now time.Time, ttl time.Duration, isExit bool) bool {
	return !isExit && ttl > 0 && !in.CreatedTS.IsZero() && now.Sub(in.CreatedTS) > ttl
}
