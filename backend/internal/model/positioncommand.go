package model

import (
	"errors"
	"time"
)

// Position command actions.
const (
	PositionSetLevels = "set_levels"
	PositionExit      = "exit"
)

// PositionCommand is a dashboard edit of an open position, sent on
// PositionCommandChannel. Levels are paise; a zero level is left
// unchanged and FNOTarget -1 removes the premium target. Real-order
// strategies are refused by stratengine.
type PositionCommand struct {
	Strategy    string    `json:"strategy"`
	Side        string    `json:"side"` // CALL or PUT
	FNOToken    string    `json:"fno_token,omitempty"`
	Action      string    `json:"action"`
	IndexStop   int64     `json:"index_stop,omitempty"`
	IndexTarget int64     `json:"index_target,omitempty"`
	FNOSL       int64     `json:"fno_sl,omitempty"`
	FNOTarget   int64     `json:"fno_target,omitempty"`
	TS          time.Time `json:"ts"`
}

// Validate checks the action and the levels.
func (c PositionCommand) Validate() error {
	if c.Strategy == "" {
		return errors.New("strategy missing")
	}
	if c.Side != "CALL" && c.Side != "PUT" {
		return errors.New("side must be CALL or PUT")
	}
	switch c.Action {
	case PositionExit:
		return nil
	case PositionSetLevels:
		if c.IndexStop < 0 || c.IndexTarget < 0 || c.FNOSL < 0 || c.FNOTarget < -1 {
			return errors.New("negative level")
		}
		if c.IndexStop == 0 && c.IndexTarget == 0 && c.FNOSL == 0 && c.FNOTarget == 0 {
			return errors.New("no level to set")
		}
		return nil
	}
	return errors.New("unknown action " + c.Action)
}
