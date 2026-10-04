package model

import "time"

// PositionContext describes one open option position for services outside
// stratengine (exitwatch). stratengine publishes the open set on
// pub:poscontext and stores it at poscontext:latest. Prices are paise.
type PositionContext struct {
	Strategy      string    `json:"strategy"`
	Side          string    `json:"side"` // CALL or PUT
	IndexToken    string    `json:"index_token"`
	IndexEntry    int64     `json:"index_entry"`
	TargetLevel   int64     `json:"target_level"`
	StopLevel     int64     `json:"stop_level"`
	FNOToken      string    `json:"fno_token"` // bare token, no exchange prefix
	FNOEntryPrice int64     `json:"fno_entry_price"`
	EntryTS       time.Time `json:"entry_ts,omitempty"`
}

// Key identifies a position across updates.
func (p PositionContext) Key() string { return p.Strategy + "|" + p.Side + "|" + p.FNOToken }

// PositionContextSet is the pub:poscontext payload: every open position.
type PositionContextSet struct {
	Positions []PositionContext `json:"positions"`
	TS        time.Time         `json:"ts"`
}
