package model

import "time"

// TGStatus is the tgsignal service's health for the dashboard.
type TGStatus struct {
	UpdatedAt time.Time        `json:"updated_at"`
	Telegram  TGTelegramStatus `json:"telegram"`
	LLM       TGLLMStatus      `json:"llm"`
	// Verdicts counts today's messages by verdict (SENT, FOLLOW_UP, NOISE, ...).
	Verdicts map[string]int `json:"verdicts"`
}

// TGTelegramStatus is the Telegram user session.
type TGTelegramStatus struct {
	Connected     bool      `json:"connected"`
	User          string    `json:"user,omitempty"`
	Chats         []int64   `json:"chats"`
	Since         time.Time `json:"since,omitzero"`           // connected at
	LastMessageAt time.Time `json:"last_message_at,omitzero"` // last message from a followed chat
	LastError     string    `json:"last_error,omitempty"`
}

// TGLLMStatus is the LLM reader.
type TGLLMStatus struct {
	Enabled     bool      `json:"enabled"`
	Model       string    `json:"model,omitempty"`
	Provider    string    `json:"provider,omitempty"` // API host
	OK          bool      `json:"ok"`                 // last read succeeded
	LastReadAt  time.Time `json:"last_read_at,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	ReadsToday  int       `json:"reads_today"`
	ErrorsToday int       `json:"errors_today"`
	TokensToday int       `json:"tokens_today"`
	AvgTokens   int       `json:"avg_tokens"`
}

// TGMessageView is one message read, as the dashboard shows it.
type TGMessageView struct {
	ChatID  int64     `json:"chat_id"`
	MsgID   int       `json:"msg_id"`
	ReplyTo int       `json:"reply_to,omitempty"`
	Edited  bool      `json:"edited,omitempty"`
	SentAt  time.Time `json:"sent_at"`
	Text    string    `json:"text"`
	English string    `json:"english,omitempty"` // the LLM's English reading
	Verdict string    `json:"verdict"`
	Reason  string    `json:"reason,omitempty"`
	Summary string    `json:"summary,omitempty"` // what was read: "BUY NIFTY 22800PE above 172 SL 166"
	Outcome string    `json:"outcome,omitempty"` // filled by the gateway from TGOutcomesKey
	// ReceivedAt and ReadMS time the read: receipt and regex + LLM
	// duration. Timeline is what stratengine did, with ms since receipt
	// (filled by the gateway from TGTimelineKey).
	ReceivedAt time.Time `json:"received_at,omitzero"`
	ReadMS     int64     `json:"read_ms,omitempty"`
	Timeline   []TGStage `json:"timeline,omitempty"`
}

// TGStage is one step of a call after it was read, ms after receipt.
type TGStage struct {
	Event string `json:"event"`
	MS    int64  `json:"ms"`
}

// Key is the message's TGSignal/TGFollowUp key.
func (m TGMessageView) Key() string { return TGMsgKey(m.ChatID, m.MsgID) }
