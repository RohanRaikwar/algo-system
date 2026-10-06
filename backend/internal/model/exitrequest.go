package model

import "time"

// ExitRequestChannel carries exitwatch's requests to close a position
// (stratengine acts on them for the strategies it allows).
const ExitRequestChannel = "cmd:exitwatch:exit"

// ExitRequest is exitwatch asking stratengine to close one open position.
type ExitRequest struct {
	Strategy   string    `json:"strategy"`
	Side       string    `json:"side"` // CALL or PUT
	IndexToken string    `json:"index_token"`
	FNOToken   string    `json:"fno_token"`
	Reason     string    `json:"reason"`
	TS         time.Time `json:"ts"`
}

// RealOrderStrategy can never be closed by exitwatch: its orders are real.
const RealOrderStrategy = "NIFTY50_FNO"
