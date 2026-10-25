package model

import (
	"fmt"
	"time"
)

// TGSignal is one option call read from a Telegram group, e.g.
//
//	BUY NIFTY 24500 CE
//	ABOVE 120
//	TARGET 140 / 160
//	SL 100
//
// Prices are option premiums in paise. Targets are the caller's and are
// only logged; the trade uses its own target.
type TGSignal struct {
	ChatID       int64     `json:"chat_id"`
	MsgID        int       `json:"msg_id"`
	Underlying   string    `json:"underlying"`  // NIFTY, SENSEX
	Strike       int64     `json:"strike"`      // index points
	OptionType   string    `json:"option_type"` // CE or PE
	TriggerPaise int64     `json:"trigger_paise"`
	SLPaise      int64     `json:"sl_paise"`
	Targets      []int64   `json:"targets,omitempty"` // paise
	Text         string    `json:"text,omitempty"`
	ReceivedAt   time.Time `json:"received_at"`
	// Edited marks a call the caller changed after posting: a waiting
	// call takes the new levels, an open trade only a higher stop.
	Edited bool `json:"edited,omitempty"`
	// Source is how the call was read: REGEX, AGREE (regex + LLM),
	// LLM_ONLY. Review only.
	Source string `json:"source,omitempty"`
}

// Side is the position side the call buys (CE → CALL, PE → PUT).
func (s TGSignal) Side() PositionSide {
	if s.OptionType == "PE" {
		return SidePut
	}
	return SideCall
}

// Validate checks the call can be traded: a contract, a trigger and a
// stop below it.
func (s TGSignal) Validate() error {
	switch {
	case s.Underlying == "":
		return fmt.Errorf("no underlying")
	case s.Strike <= 0:
		return fmt.Errorf("no strike")
	case s.OptionType != "CE" && s.OptionType != "PE":
		return fmt.Errorf("option type %q", s.OptionType)
	case s.TriggerPaise <= 0:
		return fmt.Errorf("no entry price")
	case s.SLPaise <= 0:
		return fmt.Errorf("no stop loss")
	case s.SLPaise >= s.TriggerPaise:
		return fmt.Errorf("stop %d not below entry %d", s.SLPaise, s.TriggerPaise)
	case len(s.Targets) > 0 && s.Targets[0] <= s.TriggerPaise:
		return fmt.Errorf("target %d not above entry %d", s.Targets[0], s.TriggerPaise)
	}
	return nil
}

// Key identifies the message, so a repeated delivery is traded once.
func (s TGSignal) Key() string { return TGMsgKey(s.ChatID, s.MsgID) }

// TGMsgKey is "<chat>:<msg>", the key of a Telegram message.
func TGMsgKey(chatID int64, msgID int) string { return fmt.Sprintf("%d:%d", chatID, msgID) }
