package model

import (
	"fmt"
	"time"
)

// TGFollowUpAction is what the caller said about an earlier call.
type TGFollowUpAction string

const (
	TGExit      TGFollowUpAction = "exit"       // "exit now", "book profit"
	TGSLHit     TGFollowUpAction = "sl_hit"     // "SL hit"
	TGTargetHit TGFollowUpAction = "target_hit" // "all targets done"
	TGMoveSL    TGFollowUpAction = "move_sl"    // "SL cost", "trail SL 200"
	TGCancel    TGFollowUpAction = "cancel"     // "avoid", "cancel this call"
	// TGActivate is the caller's confirmation of a call: an "Active 🔥"
	// reply, or a price post at the entry (tgsignal trade context). Take
	// the waiting call now. Only strategies that wait for it
	// (STRAT_TG_WAIT_ACTIVE) act on it.
	TGActivate TGFollowUpAction = "activate"
)

// TGFollowUp is the caller's message about an earlier call. ReplyToMsgID
// links it to the call: the message replied to, or the call itself when
// tgsignal linked the message through the call's trade context. Without
// it the follow-up acts only when exactly one call is open and Strike
// names it.
type TGFollowUp struct {
	ChatID       int64            `json:"chat_id"`
	MsgID        int              `json:"msg_id"`
	ReplyToMsgID int              `json:"reply_to_msg_id,omitempty"`
	Action       TGFollowUpAction `json:"action"`
	NewSLPaise   int64            `json:"new_sl_paise,omitempty"` // move_sl only
	Strike       int64            `json:"strike,omitempty"`
	OptionType   string           `json:"option_type,omitempty"`
	Text         string           `json:"text,omitempty"`
	ReceivedAt   time.Time        `json:"received_at"`
}

// CallKey is the TGSignal.Key of the call replied to, "" without a reply.
func (f TGFollowUp) CallKey() string {
	if f.ReplyToMsgID == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", f.ChatID, f.ReplyToMsgID)
}

// Key identifies the follow-up message.
func (f TGFollowUp) Key() string { return fmt.Sprintf("%d:%d", f.ChatID, f.MsgID) }

// Validate checks the action is known and a stop move names a stop.
func (f TGFollowUp) Validate() error {
	switch f.Action {
	case TGExit, TGSLHit, TGTargetHit, TGCancel, TGActivate:
	case TGMoveSL:
		if f.NewSLPaise <= 0 {
			return fmt.Errorf("move_sl without a stop")
		}
	default:
		return fmt.Errorf("action %q", f.Action)
	}
	return nil
}

// TGOutcome is one thing stratengine did with a call, for the review log.
type TGOutcome struct {
	CallKey string    `json:"call_key"` // TGSignal.Key
	Event   string    `json:"event"`    // ARMED, REFUSED, ENTERED, EXIT, DROPPED, ...
	Detail  string    `json:"detail"`
	TS      time.Time `json:"ts"`
	// MS is the time since tgsignal received the call's message, when
	// stratengine knows it (0 = unknown).
	MS int64 `json:"ms,omitempty"`
}
