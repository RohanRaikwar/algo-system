package tgsignal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"trading-systemv1/internal/model"
)

// HistoryMsg is one past message (tghistory, golden test set).
type HistoryMsg struct {
	MsgID   int       `json:"msg_id"`
	ReplyTo int       `json:"reply_to,omitempty"`
	Sent    time.Time `json:"sent"`
	Text    string    `json:"text"`
}

// Explain runs the service's decision on one message and describes it.
// reader nil = regex only. slPct is TG_DEFAULT_SL_PCT.
func Explain(ctx context.Context, reader CallReader, text, replyText string, isReply bool, slPct int64) string {
	rx, rxErr := Parse(text)
	rx, rxErr = withDefaultSL(rx, rxErr, slPct)
	var d decision
	switch {
	case isReply && reActive.MatchString(text) && isCall(replyText):
		d = activeReply()
	case reader == nil:
		d = decideRegex(rx, rxErr)
	case !needsLLM(text, isReply && replyText != "" && isCall(replyText)):
		d = decision{verdict: VerdictSkipped, reason: "no trade words"}
	default:
		rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res, err := reader.Read(rctx, text, replyText)
		cancel()
		d = decide(rx, rxErr, llmWithDefaultSL(res, slPct), err)
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
	return err == nil || errors.Is(err, ErrNoSL)
}

// reActive is the caller's bare "Active 🔥🔥" reply: the call is live,
// take it. Any other words ("Active hone ka wait karna" = wait for it to
// go active) leave the message to the LLM.
var reActive = regexp.MustCompile(`(?i)^\W*active(\s+now)?\W*$`)

// activeReply is the activate follow-up for an "Active" reply to a call.
func activeReply() decision {
	return decision{verdict: VerdictFollowUp, reason: "caller says Active", followUp: &model.TGFollowUp{Action: model.TGActivate}}
}
