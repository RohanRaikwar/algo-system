package tgsignal

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// The router moves each message to the worker that owns it: the call's
// worker for a message in a trade context, else its chat's worker. A
// worker runs its messages in order, never waits for the LLM (the read
// goes to the scheduler and its result comes back as a new mailbox item),
// and code-only steps (price posts, "Active", fast exits) act at once.
//
// A message being read that may become a call is pending: replies to it
// (and edits of it) wait in the pending entry and are routed again once
// its read is decided, so a confirmation that arrives during the read is
// never lost.

// item is one message on its way through a worker.
type item struct {
	m     inMsg
	now   time.Time
	start time.Time
	entry Entry
	rx    model.TGSignal
	rxErr error
	call  bool // the regex reads a call
	// prevRead: for an edit of a message in a call's context, how the
	// original was read ("" if new).
	prevRead string
}

// chat-path routes, decided when the message arrives.
type chatPath int

const (
	pathSkip   chatPath = iota // no trade words
	pathActive                 // "Active" replying to a call we did not publish
	pathRegex                  // no LLM: the regex decides
	pathCall                   // regex call: arm now, the LLM checks after
	pathLLM                    // only the LLM can read it
)

type router struct {
	h *handler

	mu      sync.Mutex
	pending map[string][]*item // "chat:msg" being read → messages held for it
	chats   map[int64]*mailbox

	busyMu sync.Mutex
	idle   *sync.Cond
	busy   int
}

func newRouter(h *handler) *router {
	r := &router{h: h, pending: make(map[string][]*item), chats: make(map[int64]*mailbox)}
	r.idle = sync.NewCond(&r.busyMu)
	return r
}

func (r *router) add(n int) {
	r.busyMu.Lock()
	r.busy += n
	if r.busy == 0 {
		r.idle.Broadcast()
	}
	r.busyMu.Unlock()
}

// waitIdle waits until every message and LLM read is settled (tests).
func (r *router) waitIdle() {
	r.busyMu.Lock()
	for r.busy > 0 {
		r.idle.Wait()
	}
	r.busyMu.Unlock()
}

// dispatch takes one Telegram message. It never blocks on the LLM.
func (r *router) dispatch(m inMsg, now time.Time) {
	h := r.h
	if !h.chats[m.ChatID] || m.Text == "" {
		return
	}
	if h.status != nil {
		h.status.message(now)
	}
	entry := Entry{ChatID: m.ChatID, MsgID: m.MsgID, ReplyTo: m.ReplyTo, Edited: m.Edited, SentAt: m.Sent, Text: m.Text, ReceivedAt: now}
	if age := now.Sub(m.Sent); age > h.maxAge {
		entry.Verdict, entry.Reason = VerdictOld, fmt.Sprintf("%s old", age.Round(time.Second))
		r.offUpdate(func() { h.record(entry); h.show(entry, "", decision{}) })
		return
	}
	if m.Edited && !m.Posted.IsZero() && now.Sub(m.Posted) > maxEditAge {
		entry.Verdict, entry.Reason = VerdictOld, fmt.Sprintf("edit of a message posted %s ago", now.Sub(m.Posted).Round(time.Minute))
		r.offUpdate(func() { h.record(entry) })
		return
	}
	if !h.firstSeen(seenKey(m), now) {
		return
	}
	it := &item{m: m, now: now, start: time.Now(), entry: entry}
	raw, rawErr := Parse(m.Text) // once: isCall would parse again
	it.call = rawErr == nil || errors.Is(rawErr, ErrNoSL)
	it.rx, it.rxErr = withDefaultSL(raw, rawErr, h.defs)
	if it.rxErr == nil {
		it.rx = withDefaultTargets(it.rx, h.defs)
	}
	it.entry.Regex = regexView(it.rx, it.rxErr)
	r.add(1)
	r.route(it)
}

// callGone handles a call the caller withdrew (its message edited into a
// non-call, or deleted). Not yet bought: the call is cancelled (a resting
// entry order is cancelled by stratengine). Bought: the position is kept
// and only reported; exits come from the caller's follow-ups as usual.
func (r *router) callGone(tc *tradeCtx, it *item, how string) {
	h := r.h
	callID := tc.call.MsgID
	switch {
	case h.ctxs.isClosed(tc):
		r.finish(it, decision{verdict: VerdictContext, reason: fmt.Sprintf("call %d %s (already closed)", callID, how)}, "")
	case h.ctxs.isFilled(tc):
		line := fmt.Sprintf("CALL_WITHDRAWN: call %s by the caller after our entry filled; position kept", how)
		log.Printf("[tgsignal] ⚠️ %d:%d %s", tc.call.ChatID, callID, line)
		h.noteOutcome(tc.call.ChatID, callID, line)
		if h.status != nil {
			h.status.verdict(VerdictContext, time.Now())
		}
		r.finish(it, decision{verdict: VerdictContext, reason: line, callMsgID: callID}, "")
	default:
		h.ctxs.add(tc, it.m, "cancel (call "+how+")")
		d := decision{verdict: VerdictFollowUp, reason: "call " + how + " by the caller", callMsgID: callID,
			followUp: &model.TGFollowUp{Action: model.TGCancel}}
		r.finish(it, r.publish(it, d), "")
	}
}

// deleted handles messages the caller deleted in chat: a deleted call is
// withdrawn (callGone); a deleted message in a call's thread is noted.
func (r *router) deleted(chat int64, ids []int, now time.Time) {
	h := r.h
	if !h.chats[chat] {
		return
	}
	for _, id := range ids {
		if tc := h.ctxs.byCall(chat, id); tc != nil {
			m := inMsg{ChatID: chat, MsgID: id, Sent: now, Text: "(deleted)"}
			it := &item{m: m, now: now, start: time.Now(),
				entry: Entry{ChatID: chat, MsgID: id, SentAt: now, Text: m.Text, ReceivedAt: now}}
			r.add(1)
			tc.w.box.push(func() { r.callGone(tc, it, "deleted") })
			continue
		}
		id := id
		r.offUpdate(func() { h.noteOutcome(chat, id, "DELETED by the caller") })
	}
}

// checkEditedFollowUp reports an edit of a follow-up that already acted
// when the edit reads differently ("Active" → "not active yet"). Nothing
// is undone: the order may already be at the broker.
func (r *router) checkEditedFollowUp(it *item, d decision) {
	was := readAction(it.prevRead)
	if !it.m.Edited || was == "" {
		return
	}
	now := ""
	if d.followUp != nil {
		now = string(d.followUp.Action)
	}
	if now == was {
		return
	}
	if now == "" {
		now = "no action"
	}
	line := fmt.Sprintf("FOLLOW_UP_EDITED: was %s, edit reads %s — earlier action NOT undone, check the trade", was, now)
	log.Printf("[tgsignal] ⚠️ %d:%d %s", it.m.ChatID, it.m.MsgID, line)
	r.h.noteOutcome(it.m.ChatID, it.m.MsgID, line)
	if r.h.status != nil {
		r.h.status.verdict(VerdictContext, time.Now())
	}
}

// offUpdate runs log-only work (journal, dashboard) off the Telegram
// update goroutine, so a slow disk never delays the next update.
func (r *router) offUpdate(fn func()) {
	r.add(1)
	go func() {
		defer r.add(-1)
		fn()
	}()
}

func msgKey(chat int64, id int) string { return model.TGMsgKey(chat, id) }

// route sends it to its worker, or holds it behind a pending read.
func (r *router) route(it *item) {
	m := it.m
	r.mu.Lock()
	if held, ok := r.heldBy(m); ok {
		r.pending[held] = append(r.pending[held], it)
		r.mu.Unlock()
		return
	}
	// The call message itself edited into something that is no longer a
	// call: the caller withdrew it.
	if m.Edited && !it.call {
		if tc := r.h.ctxs.byCall(m.ChatID, m.MsgID); tc != nil {
			r.mu.Unlock()
			tc.w.box.push(func() { r.callGone(tc, it, "edited away") })
			return
		}
	}
	// A message in a call's trade context needs nothing else: check it
	// before the journal read below.
	if tc, link := r.h.ctxs.link(m, it.now); tc != nil && !it.call {
		r.mu.Unlock()
		if m.Edited {
			it.prevRead = r.h.ctxs.lastReadOf(tc, m.MsgID)
		}
		tc.w.box.push(func() { r.inContext(tc, link, it) })
		return
	}
	r.mu.Unlock()
	replyCall := m.ReplyTo != 0 && r.h.replyToCall(m) // journal read, outside the lock
	r.mu.Lock()
	if held, ok := r.heldBy(m); ok { // a read may have started meanwhile
		r.pending[held] = append(r.pending[held], it)
		r.mu.Unlock()
		return
	}
	path := r.chatPath(it, replyCall)
	if path == pathCall || path == pathLLM || (path == pathRegex && it.call) {
		if _, ok := r.pending[msgKey(m.ChatID, m.MsgID)]; !ok {
			r.pending[msgKey(m.ChatID, m.MsgID)] = nil
		}
	}
	box := r.chats[m.ChatID]
	if box == nil {
		box = &mailbox{}
		r.chats[m.ChatID] = box
	}
	r.mu.Unlock()
	box.push(func() { r.inChat(it, path, replyCall) })
}

// heldBy is the pending read m must wait for: the message it replies to,
// or, for an edit, the message itself. The caller holds r.mu.
func (r *router) heldBy(m inMsg) (string, bool) {
	if m.ReplyTo != 0 {
		if k := msgKey(m.ChatID, m.ReplyTo); r.has(k) {
			return k, true
		}
	}
	if m.Edited {
		if k := msgKey(m.ChatID, m.MsgID); r.has(k) {
			return k, true
		}
	}
	return "", false
}

func (r *router) has(k string) bool { _, ok := r.pending[k]; return ok }

// release ends m's pending read and routes the messages held for it.
func (r *router) release(m inMsg) {
	k := msgKey(m.ChatID, m.MsgID)
	r.mu.Lock()
	held := r.pending[k]
	delete(r.pending, k)
	r.mu.Unlock()
	for _, x := range held {
		r.route(x)
	}
}

func (r *router) chatPath(it *item, replyCall bool) chatPath {
	h := r.h
	switch {
	case reActive.MatchString(it.m.Text) && replyCall:
		return pathActive
	case h.sched == nil:
		return pathRegex
	case it.call && it.rxErr == nil:
		return pathCall
	case !needsLLM(it.m.Text, replyCall):
		return pathSkip
	}
	return pathLLM
}

// inChat handles a message outside every trade context. It runs on the
// chat's mailbox.
func (r *router) inChat(it *item, path chatPath, replyCall bool) {
	h := r.h
	switch path {
	case pathSkip:
		d := decision{verdict: VerdictSkipped, reason: "no trade words"}
		if it.rxErr == nil { // cannot happen with the keyword list; never drop a call silently
			d = decideRegex(it.rx, it.rxErr)
		}
		r.finish(it, r.publish(it, d), "")
	case pathActive:
		r.finish(it, r.publish(it, activeReply()), "")
	case pathRegex:
		r.finish(it, r.publish(it, decideRegex(it.rx, it.rxErr)), "")
		r.release(it.m)
	case pathCall:
		r.armFirst(it)
	case pathLLM:
		prio := prioNoise
		if replyCall {
			prio = prioConfirm
		} else if reContract.MatchString(strings.ToUpper(it.m.Text)) {
			prio = prioNew
		}
		r.read(prio, it, h.replyText(it.m), func(res LLMResult, err error) {
			r.chatBox(it.m.ChatID).push(func() {
				english := r.readDone(it, res, err)
				d := decide(it.rx, it.rxErr, llmWithDefaultSL(res, h.defs), err)
				if err == ErrLLMStale {
					d = decision{verdict: VerdictStale, reason: err.Error()}
				}
				r.finish(it, r.publish(it, d), english)
				r.release(it.m)
			})
		})
	}
}

func (r *router) chatBox(chat int64) *mailbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chats[chat]
}

// armFirst publishes a regex call at once and sends it to the LLM to
// check. The call buys only after the check agrees: a confirmation (read
// by code or by the LLM) that comes first is held until then. A
// disagreement or an LLM failure cancels the call. The LLM usually has
// seconds to minutes before the caller confirms, so the hold rarely
// delays an order.
func (r *router) armFirst(it *item) {
	h := r.h
	c := it.rx
	c.Source = "REGEX_FIRST"
	tc, ver, err := r.publishCall(it, c, false)
	if err != nil {
		r.finish(it, decision{verdict: VerdictError, reason: "publish: " + err.Error()}, "")
		r.release(it.m)
		return
	}
	r.release(it.m)
	r.read(prioVerify, it, h.replyText(it.m), func(res LLMResult, err error) {
		tc.w.box.push(func() { r.verified(tc, ver, it, res, err) })
	})
}

// verified applies the LLM's check of a call armed on the regex.
func (r *router) verified(tc *tradeCtx, ver int, it *item, res LLMResult, err error) {
	h := r.h
	english := r.readDone(it, res, err)
	d := decide(it.rx, it.rxErr, llmWithDefaultSL(res, h.defs), err)
	if err == nil && d.call != nil && d.call.Source == "AGREE" {
		d.call.Source = "REGEX_FIRST"
		d.reason = "armed on regex, " + d.reason
		if queued, ok := h.ctxs.verify(tc, ver); ok {
			for _, q := range queued {
				r.sendActivate(tc, q, "held confirmation, call verified")
			}
		}
		r.finish(it, d, english)
		return
	}
	why := d.reason
	if err != nil {
		d.verdict, why = VerdictError, "LLM: "+err.Error()
	} else if d.verdict == VerdictSent || d.verdict == VerdictFollowUp || d.verdict == VerdictNoise {
		d.verdict = VerdictDisagree
	}
	d.call, d.followUp = nil, nil
	switch {
	case h.ctxs.cancel(tc, ver, time.Now()):
		f := model.TGFollowUp{ChatID: it.m.ChatID, MsgID: it.m.MsgID, ReplyToMsgID: it.m.MsgID, Action: model.TGCancel, ReceivedAt: it.now, Text: it.m.Text}
		if perr := h.publishFollowUp(f); perr != nil {
			log.Printf("[tgsignal] 🚨 publish cancel %s: %v", f.Key(), perr)
		}
		d.reason = "armed on regex, cancelled: " + why
		log.Printf("[tgsignal] ✋ call %d:%d cancelled: %s", it.m.ChatID, it.m.MsgID, why)
	case h.ctxs.isConfirmed(tc):
		d.reason = "armed on regex, confirmed before the LLM check, position kept: " + why
		log.Printf("[tgsignal] ⚠️ call %d:%d: LLM check disagrees after the confirmation, position kept: %s", it.m.ChatID, it.m.MsgID, why)
		h.noteOutcome(it.m.ChatID, it.m.MsgID, "LLM_DISAGREE: confirmed before the LLM check, position kept: "+why)
		if h.status != nil {
			h.status.verdict(VerdictDisagree, time.Now())
		}
	default:
		d.reason = "armed on regex, not cancelled (held, closed or edited since): " + why
	}
	r.finish(it, d, english)
}

// reFastExit is a reply that only says to get out of the trade.
var reFastExit = regexp.MustCompile(`(?i)^[^\pL\d]*(exit( now| kar ?do| karo)?|ctc exit|exit at cost|(jaldi )?niklo( bhai)?|nikal jao( abhi)?|(trailing )?sl hit|all targets? done)[^\pL\d]*$`)

var (
	reSaysSLHit  = regexp.MustCompile(`(?i)\bsl hit\b`)
	reSaysTgtHit = regexp.MustCompile(`(?i)\btargets? done\b`)
)

// fastExitAction is the follow-up a fast exit sends.
func fastExitAction(text string) model.TGFollowUpAction {
	switch {
	case reSaysSLHit.MatchString(text):
		return model.TGSLHit
	case reSaysTgtHit.MatchString(text):
		return model.TGTargetHit
	}
	return model.TGExit
}

// auditAgrees reports whether the LLM's action matches a local one. The
// three exit actions all close the position, so they agree.
func auditAgrees(local, llm string) bool {
	exits := map[string]bool{"exit": true, "sl_hit": true, "target_hit": true}
	return local == llm || (exits[local] && exits[llm])
}

// Local formats of a reply in a call's thread: whole short messages only,
// so any extra words go to the LLM.
var (
	// reLocalCTC moves the stop to the entry: "Sl ctc kar lena", "SL cost".
	reLocalCTC = regexp.MustCompile(`(?i)^[^\pL\d]*(sl|stop ?loss) (ctc|cost|cost (pe|par)|at cost)( (kar|kr) (lena|lo|do|dena)| le aao| kar lijiye)?[^\pL\d]*$|^[^\pL\d]*ctc sl[^\pL\d]*$`)
	// reLocalSLAt moves the stop to a price: "sl 112 par le aao".
	reLocalSLAt = regexp.MustCompile(`(?i)^[^\pL\d]*(?:sl|stop ?loss) ?(?:₹|rs\.? ?)?(\d+(?:\.\d+)?) (?:pe|par|pr)( (?:kar|kr) (?:lena|lo|do|dena)| le aao| shift (?:kar|kr) (?:lena|lo|do))?[^\pL\d]*$`)
	// reLocalCancel drops a call not yet bought: "Avoid", "cancel".
	reLocalCancel = regexp.MustCompile(`(?i)^[^\pL\d]*(avoid|cancel|skip|call cancel|avoid (it|this|karo|kare|kijiye)|skip (it|this|karo|kare))[^\pL\d]*$`)
)

// localSLAt is the stop price of a reLocalSLAt message in paise, 0 if
// none.
func localSLAt(text string) int64 {
	sm := reLocalSLAt.FindStringSubmatch(text)
	if sm == nil {
		return 0
	}
	return toPaise(sm[1])
}

// inContext handles a message linked to tc. It runs on tc's mailbox.
func (r *router) inContext(tc *tradeCtx, link ctxLink, it *item) {
	h := r.h
	m := it.m
	callID := tc.call.MsgID
	price := pricePost(m.Text)
	switch {
	case link == linkReply && h.ctxs.isClosed(tc):
		h.ctxs.add(tc, m, "call closed")
		r.finish(it, decision{verdict: VerdictContext, reason: fmt.Sprintf("call %d is closed", callID)}, "")
		return
	case link == linkReply && price > 0:
		if h.ctxs.addPrice(tc, m, price) {
			r.activate(tc, it, "caller's price "+rupees(price)+" reached entry "+rupees(tc.call.TriggerPaise))
			return
		}
		r.finish(it, decision{verdict: VerdictContext, reason: "caller's price " + rupees(price), callMsgID: callID}, "")
		return
	case link == linkReply && reActive.MatchString(m.Text):
		if !h.ctxs.claimActivate(tc) {
			h.ctxs.add(tc, m, "active (already confirmed)")
			r.finish(it, decision{verdict: VerdictContext, reason: "Active: call already confirmed, held or closed", callMsgID: callID}, "")
			return
		}
		h.ctxs.add(tc, m, "activate")
		r.activate(tc, it, "caller says Active")
		return
	case link == linkReply && reFastExit.MatchString(m.Text) && h.ctxs.isHeld(tc):
		r.fastExit(tc, it)
		return
	case link == linkReply && h.ctxs.isConfirmed(tc) && reLocalCTC.MatchString(m.Text):
		r.localAct(tc, it, model.TGFollowUp{Action: model.TGMoveSL, NewSLPaise: tc.call.TriggerPaise}, "stop to cost")
		return
	case link == linkReply && h.ctxs.isConfirmed(tc) && localSLAt(m.Text) > 0:
		p := localSLAt(m.Text)
		r.localAct(tc, it, model.TGFollowUp{Action: model.TGMoveSL, NewSLPaise: p}, "stop to "+rupees(p))
		return
	case link == linkReply && !h.ctxs.isHeld(tc) && reLocalCancel.MatchString(m.Text):
		r.localAct(tc, it, model.TGFollowUp{Action: model.TGCancel}, "cancel")
		return
	case h.sched == nil:
		r.finish(it, r.publish(it, decideRegex(it.rx, it.rxErr)), "")
		return
	case link == linkOnly && !needsLLM(m.Text, false) && !reCtxWords.MatchString(m.Text):
		r.finish(it, decision{verdict: VerdictSkipped, reason: "no trade words"}, "")
		return
	case !needsLLM(m.Text, true) && !(link == linkReply && hasActionEmoji(m.Text)):
		// An emoji-only reply ("✅", "❌") is read with the context below.
		h.ctxs.add(tc, m, "none")
		r.finish(it, decision{verdict: VerdictContext, reason: "no words", callMsgID: callID}, "")
		return
	}

	prio := prioConfirm
	if h.ctxs.isHeld(tc) {
		prio = prioExit
	}
	seq := tc.w.nextSeq
	tc.w.nextSeq++
	ctxText := h.ctxs.prompt(tc, link)
	apply := func(res LLMResult, err error) {
		tc.w.box.push(func() {
			tc.w.apply(seq, func() { r.contextRead(tc, link, it, res, err) })
		})
	}
	if h.ctxSched == nil {
		r.read(prio, it, ctxText, apply)
		return
	}
	r.readOn(h.ctxSched, prio, it, ctxText, func(res LLMResult, err error) {
		why := escalate(res, err)
		if why == "" {
			apply(res, err)
			return
		}
		if h.status != nil && err == nil {
			h.status.llmRead(res.Usage.Total, nil, time.Now())
		}
		log.Printf("[tgsignal] ⤴️ %d:%d fast read %s: main model reads it", it.m.ChatID, it.m.MsgID, why)
		r.read(prio, it, ctxText, apply)
	})
}

// escalate says why the fast model's reading of a context message goes
// to the main model, "" to use it: an error (not a stale queue), low
// confidence, or a new call (the fast schema has no call fields).
func escalate(res LLMResult, err error) string {
	switch {
	case err == ErrLLMStale:
		return ""
	case err != nil:
		return "failed (" + err.Error() + ")"
	case res.Kind == "new_call":
		return "says new call"
	case res.Confidence == "low":
		return "low confidence"
	}
	return ""
}

// contextRead applies the LLM's reading of a context message, in message
// order.
func (r *router) contextRead(tc *tradeCtx, link ctxLink, it *item, res LLMResult, err error) {
	h := r.h
	callID := tc.call.MsgID
	english := r.readDone(it, res, err)
	d := decide(it.rx, it.rxErr, llmWithDefaultSL(res, h.defs), err)
	if err == ErrLLMStale {
		d = decision{verdict: VerdictStale, reason: err.Error()}
	}
	activate := false
	if f := d.followUp; f != nil {
		switch {
		case link == linkOnly && !res.AboutCall:
			d = decision{verdict: VerdictNoise, reason: fmt.Sprintf("not about call %d: %s", callID, res.Reason)}
		case f.Action == model.TGActivate && !h.ctxs.claimActivate(tc):
			d = decision{verdict: VerdictContext, reason: "activate: call already confirmed, held or closed: " + res.Reason}
		case f.Action == model.TGActivate:
			activate = true
		}
		d.callMsgID = callID
	}
	read := d.what()
	if read == "" {
		read = "none"
	}
	if d.followUp != nil || link == linkReply || res.AboutCall {
		h.ctxs.add(tc, it.m, read)
	}
	if activate {
		r.activateAs(tc, it, d, english)
		return
	}
	r.finish(it, r.publish(it, d), english)
}

// activate sends the call's confirmation at once, or holds it until the
// LLM has verified the call.
func (r *router) activate(tc *tradeCtx, it *item, why string) {
	r.activateAs(tc, it, confirmed(tc.call.MsgID, why), "")
}

func (r *router) activateAs(tc *tradeCtx, it *item, d decision, english string) {
	if !r.h.ctxs.isVerified(tc) {
		held := it.m
		held.Received = it.now
		r.h.ctxs.hold(tc, held)
		r.finish(it, decision{verdict: VerdictContext, reason: d.reason + "; held until the LLM checks the call", callMsgID: tc.call.MsgID}, english)
		return
	}
	r.finish(it, r.publish(it, d), english)
}

// sendActivate sends a held confirmation once the call is verified.
func (r *router) sendActivate(tc *tradeCtx, m inMsg, why string) {
	h := r.h
	recv := m.Received
	if recv.IsZero() {
		recv = time.Now()
	}
	f := model.TGFollowUp{ChatID: m.ChatID, MsgID: m.MsgID, ReplyToMsgID: tc.call.MsgID, Action: model.TGActivate,
		ReceivedAt: recv, SentAt: m.Sent, Text: m.Text}
	if err := h.publishFollowUp(f); err != nil {
		log.Printf("[tgsignal] 🚨 publish follow-up %s: %v", f.Key(), err)
		return
	}
	log.Printf("[tgsignal] ↩️ follow-up %s → call %q: activate (%s)", f.Key(), f.CallKey(), why)
	h.noteOutcome(m.ChatID, m.MsgID, "ACTIVATE_SENT: "+why)
}

// fastExit sends the exit of a held call at once; the LLM reads the
// message after, for the log only.
func (r *router) fastExit(tc *tradeCtx, it *item) {
	r.localAct(tc, it, model.TGFollowUp{Action: fastExitAction(it.m.Text)}, "fast exit")
}

// localAct sends a follow-up the code read in a local format at once; the
// LLM reads the message after, for the log only, and a different reading
// is reported as AUDIT_DISAGREE.
func (r *router) localAct(tc *tradeCtx, it *item, f model.TGFollowUp, what string) {
	h := r.h
	d := decision{verdict: VerdictFollowUp, reason: what + " (local, LLM audits)", callMsgID: tc.call.MsgID, followUp: &f}
	h.ctxs.add(tc, it.m, string(f.Action)+" (local)")
	d = r.publish(it, d)
	r.finish(it, d, "")
	if h.sched == nil {
		return
	}
	want := string(f.Action)
	m := it.m
	r.add(1)
	h.sched.submit(&llmJob{prio: prioAudit, text: m.Text, ctxText: h.ctxs.prompt(tc, linkReply), deadline: time.Now().Add(5 * time.Minute),
		done: func(res LLMResult, err error) {
			defer r.add(-1)
			if h.status != nil {
				h.status.llmRead(res.Usage.Total, err, time.Now())
			}
			if err != nil {
				return
			}
			switch {
			case res.Kind == "follow_up" && auditAgrees(want, res.Action):
				h.noteOutcome(m.ChatID, m.MsgID, "AUDIT_OK: LLM reads "+res.Action)
			default:
				line := fmt.Sprintf("%s: LLM reads %s/%s: %s", VerdictAuditDisagree, res.Kind, res.Action, res.Reason)
				log.Printf("[tgsignal] ⚠️ local %s %d:%d %s", want, m.ChatID, m.MsgID, line)
				h.noteOutcome(m.ChatID, m.MsgID, line)
				if h.status != nil {
					h.status.verdict(VerdictAuditDisagree, time.Now())
				}
			}
		}})
}

// read sends one LLM read to the scheduler. The item stays busy until
// done runs.
func (r *router) read(prio llmPrio, it *item, ctxText string, done func(LLMResult, error)) {
	r.readOn(r.h.sched, prio, it, ctxText, done)
}

// readOn queues a read on scheduler s.
func (r *router) readOn(s *llmScheduler, prio llmPrio, it *item, ctxText string, done func(LLMResult, error)) {
	ttl := r.h.maxAge
	if prio >= prioAudit {
		ttl = 5 * time.Minute
	}
	s.submit(&llmJob{prio: prio, text: it.m.Text, ctxText: ctxText, deadline: time.Now().Add(ttl), done: done})
}

// readDone records the LLM reading on the entry and status.
func (r *router) readDone(it *item, res LLMResult, err error) string {
	h := r.h
	if err == ErrLLMStale {
		it.entry.LLM = "stale: " + err.Error()
		return ""
	}
	if h.status != nil {
		h.status.llmRead(res.Usage.Total, err, time.Now())
	}
	if err != nil {
		it.entry.LLM = "error: " + err.Error()
		return ""
	}
	it.entry.LLM = res
	if res.Fast {
		log.Printf("[tgsignal] ⚡ %d:%d fast read → %s %s (%s; queue %dms, API %dms, %s)", it.m.ChatID, it.m.MsgID, res.Kind, res.Action, res.Reason, res.QueueMS, res.APIMS, res.Provider)
		return res.Reason
	}
	log.Printf("[tgsignal] 🗣 %d:%d in English: %q → %s %s (queue %dms, API %dms, %s)", it.m.ChatID, it.m.MsgID, res.English, res.Kind, res.Action, res.QueueMS, res.APIMS, res.Provider)
	return res.English
}

// publish sends d's call or follow-up. A call opens its trade context
// (verified: read by the LLM or with no LLM at all).
func (r *router) publish(it *item, d decision) decision {
	h := r.h
	m := it.m
	switch {
	case d.call != nil:
		if _, _, err := r.publishCall(it, *d.call, true); err != nil {
			d.verdict, d.reason = VerdictError, "publish: "+err.Error()
		}
	case d.followUp != nil:
		f := *d.followUp
		f.ChatID, f.MsgID, f.ReplyToMsgID, f.ReceivedAt, f.Text = m.ChatID, m.MsgID, m.ReplyTo, it.now, m.Text
		f.SentAt = m.Sent
		if d.callMsgID != 0 {
			f.ReplyToMsgID = d.callMsgID // linked through the call's context
		}
		if err := h.publishFollowUp(f); err != nil {
			d.verdict, d.reason = VerdictError, "publish: "+err.Error()
			log.Printf("[tgsignal] 🚨 publish follow-up %s: %v", f.Key(), err)
			break
		}
		log.Printf("[tgsignal] ↩️ follow-up %s → call %q: %s new_sl=%d", f.Key(), f.CallKey(), f.Action, f.NewSLPaise)
	case d.verdict == VerdictDisagree || d.verdict == VerdictRefused || d.verdict == VerdictError:
		log.Printf("[tgsignal] ⚠️ %d:%d %s: %s — %q", m.ChatID, m.MsgID, d.verdict, d.reason, m.Text)
	}
	return d
}

// publishCall publishes a call and opens (or updates) its context.
func (r *router) publishCall(it *item, c model.TGSignal, verified bool) (*tradeCtx, int, error) {
	h := r.h
	m := it.m
	c = withDefaultTargets(c, h.defs)
	c.ChatID, c.MsgID, c.ReceivedAt, c.Text, c.Edited = m.ChatID, m.MsgID, it.now, m.Text, m.Edited
	c.SentAt, c.ReadMS, c.PublishedAt = m.Sent, time.Since(it.start).Milliseconds(), time.Now()
	if err := h.publishCall(c); err != nil {
		log.Printf("[tgsignal] 🚨 publish %s: %v", c.Key(), err)
		return nil, 0, err
	}
	tc, ver := h.ctxs.open(c, verified, it.now)
	log.Printf("[tgsignal] 📨 call %s (%s%s): BUY %s %d%s above %d SL %d (their targets %v) — sent→recv %dms, read %dms",
		c.Key(), c.Source, editTag(m.Edited), c.Underlying, c.Strike, c.OptionType, c.TriggerPaise, c.SLPaise, c.Targets,
		it.now.Sub(m.Sent).Milliseconds(), c.ReadMS)
	return tc, ver, nil
}

// finish logs the message's verdict and ends its work.
func (r *router) finish(it *item, d decision, english string) {
	r.checkEditedFollowUp(it, d)
	it.entry.Verdict, it.entry.Reason = d.verdict, d.reason
	it.entry.ReadMS = time.Since(it.start).Milliseconds()
	r.h.record(it.entry)
	r.h.show(it.entry, english, d)
	r.add(-1)
}
