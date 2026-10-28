package tgsignal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// A trade context is one published call and every later message about it:
// the caller's price posts ("➡️280🔥🔥🔥"), "Active", stop moves, exits,
// and what stratengine did with the call. A message linked to a context is
// read with the whole context, so "Abi active nhi hua h wait karna" is not
// taken for a confirmation. The calls wait for the caller's confirmation
// (STRAT_TG_WAIT_ACTIVE); the context sends it.

// ctxMsg is one message in a context and what was made of it.
type ctxMsg struct {
	At   time.Time
	Text string
	Read string // "price 280", "activate", "none", ...
}

// tradeCtx is one call's context.
type tradeCtx struct {
	call    model.TGSignal
	msgs    []ctxMsg
	prices  []int64      // the caller's price posts (paise)
	msgIDs  map[int]bool // the call and every message linked to it
	active  bool         // stratengine reported ACTIVE
	held    bool         // stratengine reported ENTERED
	closed  bool         // EXIT, DROPPED, REFUSED or ENTRY_REFUSED
	closeAt time.Time
	// confirmSent: an activate was published and not refused; later price
	// posts do not send another.
	confirmSent bool
	state       string // last stratengine event, "" before the first
	// verified: the LLM agreed with the call (or the call was read by the
	// LLM, or the regex alone with no LLM). Until then a confirmation is
	// held in queued, not sent. version counts edits of the call.
	verified bool
	version  int
	queued   []inMsg
	// w orders the call's messages; only its mailbox touches it.
	w *ctxWorker
}

// ctxWorker runs one call's messages in order. LLM readings come back in
// any order and are applied in the order their messages came (ready,
// applySeq).
type ctxWorker struct {
	box      mailbox
	nextSeq  uint64
	applySeq uint64
	ready    map[uint64]func()
}

func newCtxWorker() *ctxWorker { return &ctxWorker{ready: make(map[uint64]func())} }

// apply runs f as reading seq once every earlier reading ran. Call it
// from the worker's mailbox.
func (w *ctxWorker) apply(seq uint64, f func()) {
	w.ready[seq] = f
	for {
		g, ok := w.ready[w.applySeq]
		if !ok {
			return
		}
		delete(w.ready, w.applySeq)
		w.applySeq++
		g()
	}
}

// ctxMsgsKept is how many messages the prompt shows.
const ctxMsgsKept = 10

// closedCtxKept is how long a closed context still links late replies.
const closedCtxKept = time.Hour

// contexts holds the open (and recently closed) contexts of every chat.
type contexts struct {
	mu     sync.Mutex
	byChat map[int64][]*tradeCtx
}

func newContexts() *contexts { return &contexts{byChat: make(map[int64][]*tradeCtx)} }

// open starts the context of a published call; verified as for
// tradeCtx.verified. An edit of the call updates its levels and, unless
// the call is held, needs its own check.
func (cs *contexts) open(c model.TGSignal, verified bool, now time.Time) (*tradeCtx, int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.prune(c.ChatID, now)
	for _, tc := range cs.byChat[c.ChatID] {
		if tc.call.MsgID == c.MsgID {
			tc.call = c
			tc.version++
			if !tc.held {
				tc.verified = verified
			}
			return tc, tc.version
		}
	}
	tc := &tradeCtx{call: c, msgIDs: map[int]bool{c.MsgID: true}, verified: verified, w: newCtxWorker()}
	cs.byChat[c.ChatID] = append(cs.byChat[c.ChatID], tc)
	return tc, 0
}

// isVerified reports tc.verified.
func (cs *contexts) isVerified(tc *tradeCtx) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return tc.verified
}

// isHeld reports whether tc's call is bought and not closed.
func (cs *contexts) isHeld(tc *tradeCtx) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return tc.held && !tc.closed
}

// hold keeps a confirmation until the call is verified.
func (cs *contexts) hold(tc *tradeCtx, m inMsg) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	tc.queued = append(tc.queued, m)
}

// verify marks version ver of the call checked and returns the held
// confirmations to send. A stale version changes nothing.
func (cs *contexts) verify(tc *tradeCtx, ver int) ([]inMsg, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if ver != tc.version || tc.closed {
		return nil, false
	}
	tc.verified = true
	q := tc.queued
	tc.queued = nil
	return q, true
}

// cancel closes tc after the LLM refused its call and drops its held
// confirmations. It reports false for a stale version, a closed call, or
// a call whose confirmation already went out (active or held): stratengine
// may be buying it, and a cancel of a held call sells it.
func (cs *contexts) cancel(tc *tradeCtx, ver int, now time.Time) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	sent := tc.confirmSent && len(tc.queued) == 0
	if ver != tc.version || tc.held || tc.active || tc.closed || sent {
		return false
	}
	tc.closed, tc.closeAt, tc.queued, tc.state = true, now, nil, "CANCELLED"
	return true
}

// prune forgets contexts closed more than closedCtxKept ago. The caller
// holds cs.mu.
func (cs *contexts) prune(chat int64, now time.Time) {
	kept := cs.byChat[chat][:0]
	for _, tc := range cs.byChat[chat] {
		if tc.closed && now.Sub(tc.closeAt) > closedCtxKept {
			continue
		}
		kept = append(kept, tc)
	}
	cs.byChat[chat] = kept
}

// ctxLink is how a message is linked to a context.
type ctxLink int

const (
	linkNone  ctxLink = iota
	linkReply         // replies to the call or a message linked to it
	linkOnly          // no reply, and it is the chat's only open context
)

// link finds m's context: by reply chain, else the chat's only open
// context for a message that replies to nothing. A reply to an unknown
// message is not linked.
func (cs *contexts) link(m inMsg, now time.Time) (*tradeCtx, ctxLink) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.prune(m.ChatID, now)
	list := cs.byChat[m.ChatID]
	if m.ReplyTo != 0 {
		for _, tc := range list {
			if tc.msgIDs[m.ReplyTo] {
				return tc, linkReply
			}
		}
		return nil, linkNone
	}
	var only *tradeCtx
	for _, tc := range list {
		if tc.closed {
			continue
		}
		if only != nil {
			return nil, linkNone
		}
		only = tc
	}
	if only == nil {
		return nil, linkNone
	}
	return only, linkOnly
}

// add records a message in tc.
func (cs *contexts) add(tc *tradeCtx, m inMsg, read string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	tc.msgIDs[m.MsgID] = true
	tc.msgs = append(tc.msgs, ctxMsg{At: m.Sent, Text: m.Text, Read: read})
}

// addPrice records a caller's price post. It reports whether it confirms
// the call: at or above the entry, and no confirmation sent, the call not
// active, held or closed. A confirming post marks the confirmation sent.
func (cs *contexts) addPrice(tc *tradeCtx, m inMsg, price int64) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	tc.msgIDs[m.MsgID] = true
	tc.prices = append(tc.prices, price)
	confirm := price >= tc.call.TriggerPaise && tc.canActivate()
	read := "price " + rupees(price)
	if confirm {
		tc.confirmSent = true
		read += " → activate"
	}
	tc.msgs = append(tc.msgs, ctxMsg{At: m.Sent, Text: m.Text, Read: read})
	return confirm
}

// canActivate: an activate would still mean something. The caller holds
// cs.mu.
func (tc *tradeCtx) canActivate() bool {
	return !tc.confirmSent && !tc.active && !tc.held && !tc.closed
}

// claimActivate marks the confirmation sent if it may be; false = drop it.
func (cs *contexts) claimActivate(tc *tradeCtx) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !tc.canActivate() {
		return false
	}
	tc.confirmSent = true
	return true
}

// isClosed reports whether tc's call is over.
func (cs *contexts) isClosed(tc *tradeCtx) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return tc.closed
}

// outcome applies a stratengine event to the call's context.
func (cs *contexts) outcome(callKey, event string, now time.Time) {
	chat, id, ok := splitCallKey(callKey)
	if !ok {
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, tc := range cs.byChat[chat] {
		if tc.call.MsgID == id {
			tc.apply(event, now)
			return
		}
	}
}

// apply updates tc for one stratengine event. The caller holds cs.mu.
func (tc *tradeCtx) apply(event string, now time.Time) {
	switch event {
	case TGEventActive:
		tc.active = true
	case TGEventEntered, TGEventFilled:
		tc.held = true
	case TGEventExit, TGEventDropped, TGEventRefused, TGEventEntryRefused:
		if !tc.closed {
			tc.closed, tc.closeAt = true, now
		}
	case TGEventFollowUpIgnored:
		// stratengine did not take the follow-up (e.g. an activate before
		// the call was armed): a later price post may confirm again.
		tc.confirmSent = false
	case TGEventFirstTick:
		return // not a state
	}
	tc.state = event
}

// stratengine's outcome events the context follows (TGOutcome.Event).
const (
	TGEventArmed           = "ARMED"
	TGEventActive          = "ACTIVE"
	TGEventEntered         = "ENTERED"
	TGEventFilled          = "FILLED"
	TGEventExit            = "EXIT"
	TGEventDropped         = "DROPPED"
	TGEventRefused         = "REFUSED"
	TGEventEntryRefused    = "ENTRY_REFUSED"
	TGEventFollowUpIgnored = "FOLLOW_UP_IGNORED"
	TGEventFirstTick       = "FIRST_TICK"
)

// ctxHeader starts a trade-context block; Read puts the new message after
// it instead of treating it as a replied-to message.
const ctxHeader = "Trade context"

// prompt is tc as the LLM reads it, newest message last.
func (cs *contexts) prompt(tc *tradeCtx, link ctxLink) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c := tc.call
	var b strings.Builder
	b.WriteString(ctxHeader + "\n")
	fmt.Fprintf(&b, "Call %s (msg %d): BUY %s %d %s above %s SL %s", hhmm(c.SentAt), c.MsgID, c.Underlying, c.Strike, c.OptionType, rupees(c.TriggerPaise), rupees(c.SLPaise))
	if c.SLDefault {
		b.WriteString(" (our default)")
	}
	if len(c.Targets) > 0 {
		ts := make([]string, len(c.Targets))
		for i, t := range c.Targets {
			ts[i] = rupees(t)
		}
		b.WriteString(" targets " + strings.Join(ts, ","))
	}
	b.WriteString("\nOur state: " + tc.stateText() + "\n")
	b.WriteString("Caller price posts: " + pricesText(tc.prices) + "\n")
	msgs := tc.msgs
	if len(msgs) > ctxMsgsKept {
		msgs = msgs[len(msgs)-ctxMsgsKept:]
	}
	if len(msgs) > 0 {
		b.WriteString("Earlier messages:\n")
		for _, m := range msgs {
			fmt.Fprintf(&b, "%s %q → %s\n", hhmm(m.At), clip(m.Text, 160), m.Read)
		}
	}
	if link == linkReply {
		b.WriteString("The new message replies to this call's thread.")
	} else {
		b.WriteString("The new message replies to nothing; it may or may not be about this call.")
	}
	return b.String()
}

// stateText says where the call stands. The caller holds cs.mu.
func (tc *tradeCtx) stateText() string {
	switch {
	case tc.closed:
		return "closed (" + tc.state + ")"
	case tc.held:
		return "bought, position open"
	case tc.active || tc.confirmSent:
		return "confirmed, buying"
	}
	return "waiting for the caller to confirm the entry"
}

// pricesText is "none" or "280 … 350 (9 posts)".
func pricesText(p []int64) string {
	switch len(p) {
	case 0:
		return "none"
	case 1:
		return rupees(p[0])
	}
	return fmt.Sprintf("%s … %s (%d posts)", rupees(p[0]), rupees(p[len(p)-1]), len(p))
}

func hhmm(t time.Time) string {
	if t.IsZero() {
		return "--:--"
	}
	return t.In(time.Local).Format("15:04")
}

// clip shortens s to n runes on one line.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// rePricePost is a caller's bare price post: one number, no letters
// ("➡️280🔥🔥🔥", "104 🔥🔥🔥").
var rePricePost = regexp.MustCompile(`^[^\pL\d]*(\d+(?:\.\d+)?)[^\pL\d]*$`)

// pricePost returns the premium (paise) of a bare price post, 0 if text is
// not one.
func pricePost(text string) int64 {
	m := rePricePost.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	return toPaise(m[1])
}

// reCtxWords mark a message that replies to nothing as worth reading
// with the chat's open context, besides llmKeywords.
var reCtxWords = regexp.MustCompile(`(?i)\bactive\b`)

// load reopens today's calls that were armed or held and not closed, with
// their linked replies, after a restart.
func (cs *contexts) load(j *Journal, chats []int64, slPct int64, now time.Time) (int, error) {
	if j == nil {
		return 0, nil
	}
	y, mo, d := now.In(time.Local).Date()
	day := time.Date(y, mo, d, 0, 0, 0, 0, time.Local).UTC()
	n := 0
	for _, chat := range chats {
		rows, err := j.since(chat, day)
		if err != nil {
			return n, err
		}
		var open []*tradeCtx
		for _, r := range rows {
			if r.verdict == VerdictSent {
				c, ok := r.call(chat, slPct)
				if !ok || !reopen(r.outcome) {
					continue
				}
				tc := &tradeCtx{call: c, msgIDs: map[int]bool{c.MsgID: true}, verified: true, w: newCtxWorker()}
				for _, ev := range outcomeEvents(r.outcome) {
					tc.apply(ev, now)
				}
				open = append(open, tc)
				continue
			}
			for _, tc := range open {
				if r.replyTo != 0 && tc.msgIDs[r.replyTo] {
					tc.msgIDs[r.msgID] = true
					m := ctxMsg{At: r.sent, Text: r.text, Read: strings.ToLower(r.verdict)}
					if p := pricePost(r.text); p > 0 {
						tc.prices = append(tc.prices, p)
						m.Read = "price " + rupees(p)
					}
					tc.msgs = append(tc.msgs, m)
					break
				}
			}
		}
		cs.mu.Lock()
		cs.byChat[chat] = append(cs.byChat[chat], open...)
		cs.mu.Unlock()
		n += len(open)
	}
	return n, nil
}

// reopen: the outcome log shows the call armed or held and not closed.
func reopen(outcome string) bool {
	live, over := false, false
	for _, ev := range outcomeEvents(outcome) {
		switch ev {
		case TGEventArmed, "REPLACED", TGEventEntered, TGEventActive:
			live = true
		case TGEventExit, TGEventDropped, TGEventRefused, TGEventEntryRefused:
			over = true
		}
	}
	return live && !over
}

// outcomeEvents are the events of an outcome column ("15:04:05 EVENT
// +12ms: detail | ...").
func outcomeEvents(outcome string) []string {
	var out []string
	for _, line := range strings.Split(outcome, " | ") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			out = append(out, strings.TrimSuffix(f[1], ":"))
		}
	}
	return out
}

// journalRow is one logged message, for load.
type journalRow struct {
	msgID, replyTo int
	sent           time.Time
	text, llm      string
	verdict        string
	outcome        string
}

// call rebuilds the published call of a SENT row.
func (r journalRow) call(chat int64, slPct int64) (model.TGSignal, bool) {
	rx, err := Parse(r.text)
	c, err := withDefaultSL(rx, err, slPct)
	if err != nil && r.llm != "" {
		var res LLMResult
		if json.Unmarshal([]byte(r.llm), &res) == nil && res.Kind == "new_call" {
			c = llmWithDefaultSL(res, slPct).Call()
			err = c.Validate()
		}
	}
	c.ChatID, c.MsgID, c.SentAt, c.Text = chat, r.msgID, r.sent, r.text
	return c, err == nil
}

// since returns chat's messages sent at or after t, oldest first.
func (j *Journal) since(chat int64, t time.Time) ([]journalRow, error) {
	rows, err := j.db.Query(`SELECT msg_id, reply_to, sent_at, text, llm, verdict, outcome FROM messages
		WHERE chat_id = ? AND sent_at >= ? ORDER BY id`, chat, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []journalRow
	for rows.Next() {
		var r journalRow
		var sent sql.NullTime
		if err := rows.Scan(&r.msgID, &r.replyTo, &sent, &r.text, &r.llm, &r.verdict, &r.outcome); err != nil {
			return out, err
		}
		r.sent = sent.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// mailbox runs closures one at a time, in order, on a goroutine that
// exists only while there is work. push never blocks.
type mailbox struct {
	mu      sync.Mutex
	items   []func()
	running bool
}

func (b *mailbox) push(f func()) {
	b.mu.Lock()
	b.items = append(b.items, f)
	if !b.running {
		b.running = true
		go b.run()
	}
	b.mu.Unlock()
}

func (b *mailbox) run() {
	for {
		b.mu.Lock()
		if len(b.items) == 0 {
			b.running = false
			b.mu.Unlock()
			return
		}
		f := b.items[0]
		b.items[0] = nil
		b.items = b.items[1:]
		b.mu.Unlock()
		f()
	}
}
