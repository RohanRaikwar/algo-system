package tgsignal

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// inMsg is one Telegram message, new or edited.
type inMsg struct {
	ChatID  int64
	MsgID   int
	ReplyTo int // message replied to, 0 if none
	Text    string
	Sent    time.Time // send time, or edit time for an edit
	Posted  time.Time // original send time (edits only)
	Edited  bool
}

// maxEditAge is how old a message may be for its edit to count: an edit
// to an old call never trades.
const maxEditAge = 30 * time.Minute

// handler turns group messages into calls and follow-ups: allowlisted
// chats only, fresh messages only, each message (or edit) once. With a
// reader, every message that can be an instruction is read by the LLM and
// checked against the regex parser (decide).
type handler struct {
	chats   map[int64]bool
	maxAge  time.Duration
	slPct   int64      // default stop % for calls without one, 0 = refuse
	reader  CallReader // nil = regex only
	journal *Journal   // nil = no review log

	publishCall     func(model.TGSignal) error
	publishFollowUp func(model.TGFollowUp) error

	status *statusTracker              // nil = not tracked
	sink   func(v model.TGMessageView) // nil = not shown (dashboard list)

	mu   sync.Mutex
	seen map[string]time.Time
}

func newHandler(cfg Config, reader CallReader, journal *Journal, publishCall func(model.TGSignal) error, publishFollowUp func(model.TGFollowUp) error) *handler {
	h := &handler{
		chats: make(map[int64]bool), maxAge: cfg.MaxAge, slPct: cfg.DefaultSLPct, reader: reader, journal: journal,
		publishCall: publishCall, publishFollowUp: publishFollowUp, seen: make(map[string]time.Time),
	}
	for _, id := range cfg.ChatIDs {
		h.chats[id] = true
	}
	return h
}

func (h *handler) onMessage(ctx context.Context, m inMsg, now time.Time) {
	if !h.chats[m.ChatID] || m.Text == "" {
		return
	}
	if h.status != nil {
		h.status.message(now)
	}
	entry := Entry{ChatID: m.ChatID, MsgID: m.MsgID, ReplyTo: m.ReplyTo, Edited: m.Edited, SentAt: m.Sent, Text: m.Text, ReceivedAt: now}
	english := ""
	if age := now.Sub(m.Sent); age > h.maxAge {
		entry.Verdict, entry.Reason = VerdictOld, fmt.Sprintf("%s old", age.Round(time.Second))
		h.record(entry)
		h.show(entry, "", decision{})
		return
	}
	if m.Edited && !m.Posted.IsZero() && now.Sub(m.Posted) > maxEditAge {
		entry.Verdict, entry.Reason = VerdictOld, fmt.Sprintf("edit of a message posted %s ago", now.Sub(m.Posted).Round(time.Minute))
		h.record(entry)
		return
	}
	if !h.firstSeen(seenKey(m), now) {
		return
	}

	readStart := time.Now()
	rx, rxErr := Parse(m.Text)
	rx, rxErr = withDefaultSL(rx, rxErr, h.slPct)
	entry.Regex = regexView(rx, rxErr)
	var d decision
	switch {
	case reActive.MatchString(m.Text) && h.replyToCall(m):
		d = activeReply()
	case h.reader == nil:
		d = decideRegex(rx, rxErr)
	case !needsLLM(m.Text, h.replyToCall(m)):
		d = decision{verdict: VerdictSkipped, reason: "no trade words"}
		if rxErr == nil { // cannot happen with the keyword list; never drop a call silently
			d = decideRegex(rx, rxErr)
		}
	default:
		replyText := ""
		if m.ReplyTo != 0 && h.journal != nil {
			replyText = h.journal.TextOf(m.ChatID, m.ReplyTo)
		}
		rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res, err := h.reader.Read(rctx, m.Text, replyText)
		cancel()
		if h.status != nil {
			h.status.llmRead(res.Usage.Total, err, now)
		}
		english = res.English
		if err != nil {
			entry.LLM = "error: " + err.Error()
		} else {
			entry.LLM = res
			log.Printf("[tgsignal] 🗣 %d:%d in English: %q → %s", m.ChatID, m.MsgID, res.English, res.Kind)
		}
		d = decide(rx, rxErr, llmWithDefaultSL(res, h.slPct), err)
	}
	entry.Verdict, entry.Reason = d.verdict, d.reason
	entry.ReadMS = time.Since(readStart).Milliseconds()

	switch {
	case d.call != nil:
		c := *d.call
		c.ChatID, c.MsgID, c.ReceivedAt, c.Text, c.Edited = m.ChatID, m.MsgID, now, m.Text, m.Edited
		c.SentAt, c.ReadMS, c.PublishedAt = m.Sent, entry.ReadMS, time.Now()
		if err := h.publishCall(c); err != nil {
			entry.Verdict, entry.Reason = VerdictError, "publish: "+err.Error()
			log.Printf("[tgsignal] 🚨 publish %s: %v", c.Key(), err)
			break
		}
		log.Printf("[tgsignal] 📨 call %s (%s%s): BUY %s %d%s above %d SL %d (their targets %v) — sent→recv %dms, read %dms",
			c.Key(), c.Source, editTag(m.Edited), c.Underlying, c.Strike, c.OptionType, c.TriggerPaise, c.SLPaise, c.Targets,
			now.Sub(m.Sent).Milliseconds(), c.ReadMS)
	case d.followUp != nil:
		f := *d.followUp
		f.ChatID, f.MsgID, f.ReplyToMsgID, f.ReceivedAt, f.Text = m.ChatID, m.MsgID, m.ReplyTo, now, m.Text
		if err := h.publishFollowUp(f); err != nil {
			entry.Verdict, entry.Reason = VerdictError, "publish: "+err.Error()
			log.Printf("[tgsignal] 🚨 publish follow-up %s: %v", f.Key(), err)
			break
		}
		log.Printf("[tgsignal] ↩️ follow-up %s → call %q: %s new_sl=%d", f.Key(), f.CallKey(), f.Action, f.NewSLPaise)
	case d.verdict == VerdictDisagree || d.verdict == VerdictRefused || d.verdict == VerdictError:
		log.Printf("[tgsignal] ⚠️ %d:%d %s: %s — %q", m.ChatID, m.MsgID, d.verdict, d.reason, m.Text)
	}
	h.record(entry)
	h.show(entry, english, d)
}

// show sends the message and its verdict to the dashboard list.
func (h *handler) show(e Entry, english string, d decision) {
	if h.status != nil {
		h.status.verdict(e.Verdict, time.Now())
	}
	if h.sink == nil {
		return
	}
	h.sink(model.TGMessageView{
		ChatID: e.ChatID, MsgID: e.MsgID, ReplyTo: e.ReplyTo, Edited: e.Edited, SentAt: e.SentAt,
		Text: e.Text, English: english, Verdict: e.Verdict, Reason: e.Reason, Summary: d.summary(),
		ReceivedAt: e.ReceivedAt, ReadMS: e.ReadMS,
	})
}

// summary is a one-line description of what was read.
func (d decision) summary() string {
	switch {
	case d.call != nil:
		c := d.call
		sl := rupees(c.SLPaise)
		if c.SLDefault {
			sl += " default"
		}
		return fmt.Sprintf("BUY %s %d%s above %s SL %s (%s)", c.Underlying, c.Strike, c.OptionType, rupees(c.TriggerPaise), sl, c.Source)
	case d.followUp != nil:
		f := d.followUp
		if f.Action == model.TGMoveSL {
			return "move stop to " + rupees(f.NewSLPaise)
		}
		return string(f.Action)
	}
	return ""
}

// rupees formats paise as "172.00".
func rupees(p int64) string { return fmt.Sprintf("%d.%02d", p/100, p%100) }

// replyToCall reports whether m replies to a message that reads as a call.
func (h *handler) replyToCall(m inMsg) bool {
	if m.ReplyTo == 0 || h.journal == nil {
		return false
	}
	return isCall(h.journal.TextOf(m.ChatID, m.ReplyTo))
}

func (h *handler) record(e Entry) {
	if h.journal == nil {
		return
	}
	if err := h.journal.Record(e); err != nil {
		log.Printf("[tgsignal] journal: %v", err)
	}
}

func editTag(edited bool) string {
	if edited {
		return ", edited"
	}
	return ""
}

// seenKey is a message's identity; an edit is new when its text changes.
func seenKey(m inMsg) string {
	k := fmt.Sprintf("%d:%d", m.ChatID, m.MsgID)
	if m.Edited {
		f := fnv.New64a()
		f.Write([]byte(m.Text))
		k += fmt.Sprintf("#%x", f.Sum64())
	}
	return k
}

// firstSeen reports whether key is new, forgetting keys older than a day.
func (h *handler) firstSeen(key string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, dup := h.seen[key]; dup {
		return false
	}
	for k, t := range h.seen {
		if now.Sub(t) > 24*time.Hour {
			delete(h.seen, k)
		}
	}
	h.seen[key] = now
	return true
}

// decision is what to do with one message.
type decision struct {
	verdict  string
	reason   string
	call     *model.TGSignal
	followUp *model.TGFollowUp
}

func regexView(rx model.TGSignal, err error) any {
	switch {
	case errors.Is(err, ErrNotACall):
		return "not a call"
	case err != nil:
		return "refused: " + err.Error()
	}
	return rx
}

// decideRegex is the regex-only path (TG_LLM=off or LLM skipped).
func decideRegex(rx model.TGSignal, rxErr error) decision {
	switch {
	case errors.Is(rxErr, ErrNotACall):
		return decision{verdict: VerdictNoise, reason: "regex: not a call"}
	case rxErr != nil:
		return decision{verdict: VerdictRefused, reason: "regex: " + rxErr.Error()}
	}
	rx.Source = "REGEX"
	return decision{verdict: VerdictSent, reason: "regex", call: &rx}
}

// decide combines the regex and LLM readings. The code decides, not the
// model: a text call trades when both readers agree, or on the LLM alone
// only when the regex found no call and the LLM is sure. Any conflict is
// no trade.
func decide(rx model.TGSignal, rxErr error, llm LLMResult, llmErr error) decision {
	if llmErr != nil {
		d := decideRegex(rx, rxErr)
		d.reason = "LLM unavailable (" + llmErr.Error() + "); " + d.reason
		if d.verdict == VerdictNoise {
			d.verdict = VerdictError
		}
		return d
	}
	switch llm.Kind {
	case "new_call":
		lc := llm.Call()
		if rxErr == nil {
			if diff := callDiff(rx, lc); diff != "" {
				return decision{verdict: VerdictDisagree, reason: "regex and LLM differ: " + diff}
			}
			rx.Source = "AGREE"
			return decision{verdict: VerdictSent, reason: "regex + LLM agree: " + llm.Reason, call: &rx}
		}
		if !supported[lc.Underlying] {
			return decision{verdict: VerdictRefused, reason: fmt.Sprintf("LLM: %q not supported", lc.Underlying)}
		}
		if err := lc.Validate(); err != nil {
			return decision{verdict: VerdictRefused, reason: "LLM call: " + err.Error()}
		}
		if llm.Confidence != "high" {
			return decision{verdict: VerdictRefused, reason: "LLM-only call with " + llm.Confidence + " confidence: " + llm.Reason}
		}
		lc.Source = "LLM_ONLY"
		return decision{verdict: VerdictSent, reason: "LLM only (regex: " + rxErr.Error() + "): " + llm.Reason, call: &lc}
	case "follow_up":
		if rxErr == nil {
			return decision{verdict: VerdictDisagree, reason: "regex read a call, LLM a follow-up: " + llm.Reason}
		}
		f := llm.FollowUp()
		if err := f.Validate(); err != nil {
			return decision{verdict: VerdictRefused, reason: "follow-up: " + err.Error()}
		}
		return decision{verdict: VerdictFollowUp, reason: llm.Reason, followUp: &f}
	default:
		if rxErr == nil {
			return decision{verdict: VerdictDisagree, reason: "regex read a call, LLM says noise: " + llm.Reason}
		}
		return decision{verdict: VerdictNoise, reason: llm.Reason}
	}
}

// callDiff names the fields two readings disagree on, "" if none.
func callDiff(a, b model.TGSignal) string {
	var out string
	add := func(name string, x, y any) {
		if x != y {
			out += fmt.Sprintf("%s %v≠%v ", name, x, y)
		}
	}
	add("underlying", a.Underlying, b.Underlying)
	add("strike", a.Strike, b.Strike)
	add("type", a.OptionType, b.OptionType)
	add("entry", a.TriggerPaise, b.TriggerPaise)
	add("sl", a.SLPaise, b.SLPaise)
	return out
}
