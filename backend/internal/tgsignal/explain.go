package tgsignal

import (
	"context"
	"fmt"
	"time"
)

// HistoryMsg is one past message (tghistory, golden test set).
type HistoryMsg struct {
	MsgID   int       `json:"msg_id"`
	ReplyTo int       `json:"reply_to,omitempty"`
	Sent    time.Time `json:"sent"`
	Text    string    `json:"text"`
}

// Explain runs the service's decision on one message and describes it.
// reader nil = regex only.
func Explain(ctx context.Context, reader CallReader, text, replyText string, isReply bool) string {
	rx, rxErr := Parse(text)
	var d decision
	switch {
	case reader == nil:
		d = decideRegex(rx, rxErr)
	case !needsLLM(text, isReply && replyText != "" && isCall(replyText)):
		d = decision{verdict: VerdictSkipped, reason: "no trade words"}
	default:
		rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res, err := reader.Read(rctx, text, replyText)
		cancel()
		d = decide(rx, rxErr, res, err)
		if err == nil {
			d.reason = fmt.Sprintf("EN: %s | %s (tokens %d)", res.English, d.reason, res.Usage.Total)
		}
	}
	switch {
	case d.call != nil:
		return fmt.Sprintf("%s [%s] BUY %s %d%s above %d SL %d targets %v — %s", d.verdict, d.call.Source,
			d.call.Underlying, d.call.Strike, d.call.OptionType, d.call.TriggerPaise, d.call.SLPaise, d.call.Targets, d.reason)
	case d.followUp != nil:
		return fmt.Sprintf("%s %s new_sl=%d strike=%d — %s", d.verdict, d.followUp.Action, d.followUp.NewSLPaise, d.followUp.Strike, d.reason)
	}
	return d.verdict + " — " + d.reason
}

func isCall(text string) bool {
	_, err := Parse(text)
	return err == nil
}
