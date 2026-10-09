// Package push sends Web Push notifications for trade events. The gateway
// runs it: it listens on pub:signal and pub:refused and sends one VAPID push
// per event to every stored browser subscription. It never touches the order
// path.
package push

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Notification is the JSON payload the service worker (frontend/src/sw.ts)
// turns into a system notification.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Tag groups notifications: a new one with the same tag replaces the old.
	Tag string `json:"tag,omitempty"`
	URL string `json:"url,omitempty"`
	// Sticky keeps the notification on screen until the user acts on it.
	Sticky bool `json:"sticky,omitempty"`
}

// signalMsg is the subset of the pub:signal payload push needs. stratengine
// (BUY/SELL/EXIT) and exitwatch (WATCH_EXIT) publish the same shape.
type signalMsg struct {
	StrategyName    string `json:"strategy_name"`
	Action          string `json:"action"`
	Side            string `json:"side"`
	Price           int64  `json:"price"` // index LTP, paise
	EntryFNOPrice   int64  `json:"entry_fno_price"`
	CurrentFNOPrice int64  `json:"current_fno_price"`
	Reason          string `json:"reason"`
	FNOToken        string `json:"fno_token"`
	FNOSymbol       string `json:"fno_symbol"`
	OrderMode       string `json:"order_mode"`
}

// FormatSignal turns one pub:signal payload into a notification. ok is false
// for actions that should not notify or payloads that do not parse.
func FormatSignal(payload []byte) (n Notification, ok bool) {
	var s signalMsg
	if err := json.Unmarshal(payload, &s); err != nil {
		return Notification{}, false
	}
	inst := s.FNOSymbol
	if inst == "" {
		inst = s.Side
	}
	mode := ""
	if s.OrderMode != "" {
		mode = " [" + s.OrderMode + "]"
	}

	switch s.Action {
	case "BUY", "SELL":
		body := fmt.Sprintf("%s @ %s", inst, Rupees(s.EntryFNOPrice))
		if s.EntryFNOPrice == 0 {
			body = fmt.Sprintf("%s, index %s", inst, Rupees(s.Price))
		}
		return Notification{
			Title: fmt.Sprintf("%s %s%s", s.Action, s.StrategyName, mode),
			Body:  joinReason(body, s.Reason),
			Tag:   "entry-" + s.FNOToken,
			URL:   "/",
		}, true
	case "EXIT":
		body := fmt.Sprintf("%s @ %s", inst, Rupees(s.CurrentFNOPrice))
		if s.EntryFNOPrice > 0 && s.CurrentFNOPrice > 0 {
			body += fmt.Sprintf(" (%s/unit)", SignedRupees(s.CurrentFNOPrice-s.EntryFNOPrice))
		}
		return Notification{
			Title:  fmt.Sprintf("EXIT %s%s", s.StrategyName, mode),
			Body:   joinReason(body, s.Reason),
			Tag:    "exit-" + s.FNOToken,
			URL:    "/",
			Sticky: true,
		}, true
	case "WATCH_EXIT":
		body := fmt.Sprintf("%s @ %s", inst, Rupees(s.CurrentFNOPrice))
		if s.EntryFNOPrice > 0 && s.CurrentFNOPrice > 0 {
			body += fmt.Sprintf(" (%s/unit)", SignedRupees(s.CurrentFNOPrice-s.EntryFNOPrice))
		}
		return Notification{
			Title:  fmt.Sprintf("Exit advice %s", s.StrategyName),
			Body:   joinReason(body, s.Reason),
			Tag:    "watch-" + s.FNOToken,
			URL:    "/",
			Sticky: true,
		}, true
	}
	return Notification{}, false
}

func joinReason(body, reason string) string {
	if reason == "" {
		return body
	}
	return body + "\n" + reason
}

// refusedEntry and refusedView mirror stratengine/refused.go.
type refusedEntry struct {
	Strategy string `json:"strategy"`
	Side     string `json:"side"`
	Strike   int64  `json:"strike,omitempty"`
	Reason   string `json:"reason"`
	TS       string `json:"ts"`
}

type refusedView struct {
	Date    string         `json:"date"`
	Entries []refusedEntry `json:"entries"`
}

// RefusedTracker diffs the full-day pub:refused list so only entries added
// since the previous message notify. The list is capped and trimmed from the
// front, so entries are matched by identity, not by position. It is not safe
// for concurrent use.
type RefusedTracker struct {
	primed bool
	date   string
	seen   map[refusedEntry]bool
}

// Next returns notifications for entries new since the last call. The first
// call only records the current list: after a gateway restart the whole day
// would otherwise notify again.
func (t *RefusedTracker) Next(payload []byte) []Notification {
	var v refusedView
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil
	}
	if v.Date != t.date || t.seen == nil {
		t.date, t.seen = v.Date, make(map[refusedEntry]bool, len(v.Entries))
	}
	notify := t.primed
	t.primed = true

	var out []Notification
	for _, e := range v.Entries {
		if t.seen[e] {
			continue
		}
		t.seen[e] = true
		if !notify {
			continue
		}
		title := "Refused " + e.Strategy
		if e.Side != "" {
			title += " " + e.Side
		}
		body := e.Reason
		if e.Strike > 0 {
			body = fmt.Sprintf("strike %d: %s", e.Strike, e.Reason)
		}
		out = append(out, Notification{Title: title, Body: body, Tag: "refused-" + e.TS, URL: "/"})
	}
	return out
}

// Rupees formats paise as "₹1,234.56" using integer math only.
func Rupees(p int64) string {
	sign := ""
	if p < 0 {
		sign, p = "-", -p
	}
	return fmt.Sprintf("%s₹%s.%02d", sign, groupThousands(p/100), p%100)
}

// SignedRupees is Rupees with an explicit "+" for non-negative values.
func SignedRupees(p int64) string {
	if p >= 0 {
		return "+" + Rupees(p)
	}
	return Rupees(p)
}

func groupThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
