package tgsignal

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Journal is the review log: every message read, how it was read, the
// verdict, and what stratengine did with it.
type Journal struct {
	db *sql.DB
}

// Verdicts recorded per message.
const (
	VerdictSent     = "SENT"      // call published (Source says how it was read)
	VerdictDisagree = "DISAGREE"  // regex and LLM read different calls: no trade
	VerdictRefused  = "REFUSED"   // a call that cannot be traded (no SL, unsupported, low confidence)
	VerdictFollowUp = "FOLLOW_UP" // follow-up published
	VerdictNoise    = "NOISE"
	VerdictSkipped  = "SKIPPED" // prefilter: price tick / chat, no LLM call
	VerdictOld      = "OLD"     // older than TG_MAX_AGE
	VerdictError    = "ERROR"
)

// OpenJournal opens (or creates) the review log.
func OpenJournal(path string) (*Journal, error) {
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_sync=NORMAL")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS messages (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id    INTEGER NOT NULL,
		msg_id     INTEGER NOT NULL,
		reply_to   INTEGER NOT NULL DEFAULT 0,
		edited     INTEGER NOT NULL DEFAULT 0,
		sent_at    DATETIME NOT NULL,
		text       TEXT NOT NULL,
		regex      TEXT NOT NULL DEFAULT '',
		llm        TEXT NOT NULL DEFAULT '',
		verdict    TEXT NOT NULL,
		reason     TEXT NOT NULL DEFAULT '',
		outcome    TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_messages_msg ON messages(chat_id, msg_id);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Journal{db: db}, nil
}

// Close closes the log.
func (j *Journal) Close() error { return j.db.Close() }

// Entry is one logged message.
type Entry struct {
	ChatID  int64
	MsgID   int
	ReplyTo int
	Edited  bool
	SentAt  time.Time
	Text    string
	Regex   any // parse result or error text
	LLM     any // LLMResult, error text or nil
	Verdict string
	Reason  string
}

// Record appends one message's reading.
func (j *Journal) Record(e Entry) error {
	_, err := j.db.Exec(`INSERT INTO messages (chat_id, msg_id, reply_to, edited, sent_at, text, regex, llm, verdict, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ChatID, e.MsgID, e.ReplyTo, e.Edited, e.SentAt.UTC(), e.Text, asJSON(e.Regex), asJSON(e.LLM), e.Verdict, e.Reason)
	return err
}

// TextOf returns the latest logged text of a message, "" if unknown. Used
// to give the LLM the call a follow-up replies to.
func (j *Journal) TextOf(chatID int64, msgID int) string {
	var text string
	_ = j.db.QueryRow(`SELECT text FROM messages WHERE chat_id = ? AND msg_id = ? ORDER BY id DESC LIMIT 1`, chatID, msgID).Scan(&text)
	return text
}

// AddOutcome appends stratengine's outcome to the call's latest row.
func (j *Journal) AddOutcome(chatID int64, msgID int, line string) error {
	_, err := j.db.Exec(`UPDATE messages SET outcome = CASE WHEN outcome = '' THEN ? ELSE outcome || ' | ' || ? END
		WHERE id = (SELECT id FROM messages WHERE chat_id = ? AND msg_id = ? ORDER BY id DESC LIMIT 1)`,
		line, line, chatID, msgID)
	return err
}

func asJSON(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case error:
		return x.Error()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
