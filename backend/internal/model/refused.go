package model

// RefusedEntry is one entry signal stratengine blocked before any order.
// Strike is in index points.
type RefusedEntry struct {
	Strategy       string `json:"strategy"`
	Side           string `json:"side"`
	Token          string `json:"token"`
	Exchange       string `json:"exchange"`
	Strike         int64  `json:"strike,omitempty"`
	Reason         string `json:"reason"`          // why it was refused
	StrategyReason string `json:"strategy_reason"` // why the strategy wanted to enter
	TS             string `json:"ts"`              // RFC3339 UTC
}

// RefusedView is the RefusedChannel payload: today's refused entries,
// oldest first.
type RefusedView struct {
	Date    string         `json:"date"` // IST YYYY-MM-DD
	Entries []RefusedEntry `json:"entries"`
}
