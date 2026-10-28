package tgsignal

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// scriptReader answers by message text, after an optional gate and delay.
type scriptReader struct {
	mu    sync.Mutex
	gate  chan struct{} // nil = no wait
	delay map[string]time.Duration
	res   func(text, ctxText string) (LLMResult, error)
	calls []string
}

func (s *scriptReader) Read(_ context.Context, text, ctxText string) (LLMResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, text)
	gate, d := s.gate, s.delay[text]
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	time.Sleep(d)
	return s.res(text, ctxText)
}

func (s *scriptReader) n() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// safeSink records publishes from any goroutine.
type safeSink struct {
	mu      sync.Mutex
	calls   []model.TGSignal
	follows []model.TGFollowUp
}

func (s *safeSink) call(c model.TGSignal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	return nil
}

func (s *safeSink) follow(f model.TGFollowUp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.follows = append(s.follows, f)
	return nil
}

func (s *safeSink) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls), len(s.follows)
}

func (s *safeSink) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, f := range s.follows {
		out = append(out, string(f.Action))
	}
	return out
}

func newPipeline(t *testing.T, r CallReader, journal bool) (*handler, *safeSink, *Journal) {
	t.Helper()
	var j *Journal
	if journal {
		var err error
		if j, err = OpenJournal(filepath.Join(t.TempDir(), "tg.db")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { j.Close() })
	}
	s := &safeSink{}
	h := newHandler(Config{ChatIDs: []int64{42}, MaxAge: time.Minute}, r, j, s.call, s.follow)
	return h, s, j
}

// agreeing reads realCall as the call and anything else as noise.
func agreeing(text, _ string) (LLMResult, error) {
	if text == realCall {
		return llmCall(22750, "PE", "191", "182"), nil
	}
	return LLMResult{Kind: "noise", Action: "none", Confidence: "high", Reason: "chat"}, nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A call arms on the regex before its LLM read ends; a price reply that
// comes during the read is held, linked, and its confirmation sent only
// once the LLM agrees.
func TestPipelineArmFirstAndPendingReply(t *testing.T) {
	r := &scriptReader{gate: make(chan struct{}), res: agreeing}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.dispatch(reply(2, 1, "➡️192🔥🔥"), tNow)
	waitFor(t, "arm", func() bool { c, _ := s.counts(); return c == 1 })
	if _, f := s.counts(); f != 0 {
		t.Fatal("confirmation sent before the LLM agreed")
	}
	close(r.gate)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGActivate) {
		t.Fatalf("follow-ups %v", got)
	}
	if s.follows[0].CallKey() != "42:1" || s.follows[0].MsgID != 2 {
		t.Fatalf("activate %+v", s.follows[0])
	}
}

// The LLM disagrees: the armed call is cancelled and its held
// confirmation is never sent.
func TestPipelineDisagreeDropsHeldConfirmation(t *testing.T) {
	r := &scriptReader{gate: make(chan struct{}), res: func(string, string) (LLMResult, error) {
		return llmCall(22750, "PE", "191", "172"), nil
	}}
	h, s, _ := newPipeline(t, r, true)
	h.r.dispatch(msg(1, realCall), tNow)
	h.r.dispatch(reply(2, 1, "➡️192🔥🔥"), tNow)
	close(r.gate)
	h.r.waitIdle()
	if got := s.actions(); len(got) != 1 || got[0] != string(model.TGCancel) {
		t.Fatalf("follow-ups %v, want only a cancel", got)
	}
}

// Two LLM reads on one call apply in message order, whatever order they
// finish in.
func TestPipelineContextReadsApplyInOrder(t *testing.T) {
	r := &scriptReader{
		delay: map[string]time.Duration{"sl 195 par le aao": 60 * time.Millisecond},
		res: func(text, ctxText string) (LLMResult, error) {
			switch text {
			case "sl 195 par le aao":
				return LLMResult{Kind: "follow_up", Action: "move_sl", NewSL: "195", Confidence: "high", AboutCall: true}, nil
			case "jaldi bahar aa jao":
				return LLMResult{Kind: "follow_up", Action: "exit", Confidence: "high", AboutCall: true}, nil
			}
			return agreeing(text, ctxText)
		}}
	h, s, _ := newPipeline(t, r, true)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.ctxs.outcome("42:1", TGEventEntered, tNow)
	h.r.dispatch(reply(2, 1, "sl 195 par le aao"), tNow)
	h.r.dispatch(reply(3, 1, "jaldi bahar aa jao"), tNow)
	h.r.waitIdle()
	if got := s.actions(); strings.Join(got, ",") != "move_sl,exit" {
		t.Fatalf("follow-ups %v, want move_sl then exit", got)
	}
}

// "niklo" on a held call exits at once; the LLM's audit disagrees, which
// is logged and does not undo the exit.
func TestPipelineFastExit(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, j := newPipeline(t, r, true)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.ctxs.outcome("42:1", TGEventEntered, tNow)

	r.mu.Lock()
	r.gate = make(chan struct{})
	r.mu.Unlock()
	h.r.dispatch(reply(2, 1, "Jaldi niklo 🔥🔥"), tNow)
	waitFor(t, "fast exit", func() bool { _, f := s.counts(); return f == 1 })
	if s.follows[0].Action != model.TGExit || s.follows[0].CallKey() != "42:1" {
		t.Fatalf("exit %+v", s.follows[0])
	}
	close(r.gate)
	h.r.waitIdle()
	var outcome string
	if err := j.db.QueryRow(`SELECT outcome FROM messages WHERE msg_id = 2`).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outcome, VerdictAuditDisagree) {
		t.Fatalf("audit outcome %q", outcome)
	}
}

// On a call not yet held, exit words go to the LLM with the context.
func TestPipelineFastExitOnlyWhenHeld(t *testing.T) {
	r := &scriptReader{res: agreeing}
	h, s, _ := newPipeline(t, r, true)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.onMessage(context.Background(), reply(2, 1, "niklo"), tNow)
	if _, f := s.counts(); f != 0 || r.n() != 2 {
		t.Fatalf("waiting call: follows %d, LLM reads %d", f, r.n())
	}
}

// Both providers down: a regex call is cancelled.
func TestPipelineFailoverAndBothDown(t *testing.T) {
	busy := &scriptReader{res: func(string, string) (LLMResult, error) {
		return LLMResult{}, &rateError{wait: time.Millisecond, err: errors.New("HTTP 429")}
	}}
	fallback := &scriptReader{res: agreeing}
	h, s, _ := newPipeline(t, busy, true)
	h.sched.addProvider("fallback", fallback, llmLimits{})
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	if fallback.n() != 1 || len(s.follows) != 0 {
		t.Fatalf("failover: fallback reads %d, follows %v", fallback.n(), s.actions())
	}

	h2, s2, _ := newPipeline(t, busy, true)
	h2.sched.addProvider("fallback", busy, llmLimits{})
	h2.onMessage(context.Background(), msg(1, realCall), tNow)
	if got := s2.actions(); len(got) != 1 || got[0] != string(model.TGCancel) {
		t.Fatalf("both down: %v", got)
	}
}

// Strict priority: once the running read ends, the exit read goes before
// earlier noise; noise past its deadline is dropped as stale.
func TestSchedulerPriorityAndStale(t *testing.T) {
	gate := make(chan struct{})
	r := &scriptReader{res: agreeing}
	s := newLLMScheduler(llmLimits{Workers: 1})
	s.addProvider("p", r, llmLimits{})
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	job := func(name string, p llmPrio, ttl time.Duration) {
		wg.Add(1)
		s.submit(&llmJob{prio: p, text: name, deadline: time.Now().Add(ttl), done: func(_ LLMResult, err error) {
			mu.Lock()
			if errors.Is(err, ErrLLMStale) {
				name += ":stale"
			}
			order = append(order, name)
			mu.Unlock()
			wg.Done()
		}})
	}
	r.mu.Lock()
	r.gate = gate
	r.mu.Unlock()
	job("first", prioNoise, time.Minute)
	waitFor(t, "first read", func() bool { return r.n() == 1 })
	r.mu.Lock()
	r.gate = nil
	r.mu.Unlock()
	job("noise", prioNoise, time.Minute)
	job("old", prioNoise, 10*time.Millisecond)
	job("exit", prioExit, time.Minute)
	time.Sleep(30 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := strings.Join(order, ","); got != "first,old:stale,exit,noise" {
		t.Fatalf("order %s", got)
	}
}

// Identical prompts in flight are read once; a cached reading answers
// without a request.
func TestSchedulerCoalesceAndCache(t *testing.T) {
	gate := make(chan struct{})
	r := &scriptReader{gate: gate, res: agreeing}
	s := newLLMScheduler(llmLimits{Workers: 4, CacheTTL: time.Minute})
	s.addProvider("p", r, llmLimits{})
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		s.submit(&llmJob{prio: prioConfirm, text: "Wait for entry", ctxText: "ctx", deadline: time.Now().Add(time.Minute),
			done: func(LLMResult, error) { wg.Done() }})
	}
	close(gate)
	wg.Wait()
	wg.Add(1)
	s.submit(&llmJob{prio: prioConfirm, text: "Wait for entry", ctxText: "ctx", deadline: time.Now().Add(time.Minute),
		done: func(LLMResult, error) { wg.Done() }})
	wg.Wait()
	if r.n() != 1 {
		t.Fatalf("%d requests, want 1", r.n())
	}
}

func TestTokenBucket(t *testing.T) {
	b := newBucket(600) // 10 a second
	now := time.Unix(0, 0)
	b.refill(now)
	if b.wait(600) != 0 {
		t.Fatal("full bucket waits")
	}
	b.take(600)
	if w := b.wait(100); w < 9*time.Second || w > 11*time.Second {
		t.Fatalf("wait %s, want ~10s", w)
	}
	b.refill(now.Add(10 * time.Second))
	if b.wait(100) != 0 {
		t.Fatal("not refilled")
	}
	if (&tokenBucket{}).wait(1e9) != 0 {
		t.Fatal("unlimited bucket waits")
	}
}

// 50 chats, each with an open call and 100 replies (price posts and
// LLM-read chat), all dispatched from one goroutine like Telegram's: no
// message is lost, LLM readings apply in message order per call, and the
// code-only steps stay fast.
func TestRouterLoad(t *testing.T) {
	const chats, replies = 50, 100
	r := &scriptReader{res: func(text, _ string) (LLMResult, error) {
		if strings.Contains(text, "BUY NIFTY") {
			return llmCall(22750, "PE", "191", "182"), nil
		}
		time.Sleep(time.Millisecond)
		return LLMResult{Kind: "noise", Action: "none", Confidence: "high", AboutCall: true, Reason: "chat"}, nil
	}}
	s := &safeSink{}
	ids := make([]int64, chats)
	for i := range ids {
		ids[i] = int64(1000 + i)
	}
	h := newHandler(Config{ChatIDs: ids, MaxAge: time.Minute, LLMWorkers: 16}, r, nil, s.call, s.follow)
	var shown atomic.Int64
	var mu sync.Mutex
	var codeMS []int64
	h.sink = func(v model.TGMessageView) {
		shown.Add(1)
		if pricePost(v.Text) > 0 {
			mu.Lock()
			codeMS = append(codeMS, v.ReadMS)
			mu.Unlock()
		}
	}
	for _, chat := range ids {
		h.r.dispatch(inMsg{ChatID: chat, MsgID: 1, Text: realCall, Sent: tNow}, tNow)
	}
	for k := range replies {
		for _, chat := range ids {
			text := fmt.Sprintf("➡️%d🔥", 150+k%30) // below the 191 entry
			if k%2 == 1 {
				text = fmt.Sprintf("wait karna bhai stop loss %d", k)
			}
			h.r.dispatch(inMsg{ChatID: chat, MsgID: 2 + k, ReplyTo: 1, Text: text, Sent: tNow}, tNow)
		}
	}
	h.r.waitIdle()

	if got, want := shown.Load(), int64(chats*(replies+1)); got != want {
		t.Fatalf("%d messages settled, want %d", got, want)
	}
	for _, chat := range ids {
		tc, _ := h.ctxs.link(inMsg{ChatID: chat, MsgID: 999, ReplyTo: 1}, tNow)
		if tc == nil || len(tc.msgs) != replies {
			t.Fatalf("chat %d context %v", chat, tc)
		}
		last := -1
		for _, m := range tc.msgs {
			if !strings.HasPrefix(m.Text, "wait") {
				continue
			}
			var n int
			fmt.Sscanf(m.Text, "wait karna bhai stop loss %d", &n)
			if n < last {
				t.Fatalf("chat %d: reading of %d applied after %d", chat, n, last)
			}
			last = n
		}
	}
	sort.Slice(codeMS, func(i, j int) bool { return codeMS[i] < codeMS[j] })
	if p99 := codeMS[len(codeMS)*99/100]; p99 >= 5 {
		t.Fatalf("code-only p99 %dms", p99)
	}
}

// Providers add up: once the first one's budget is spent, reads go to the
// next one at once.
func TestSchedulerSpillsAcrossProviders(t *testing.T) {
	a := &scriptReader{res: agreeing}
	b := &scriptReader{res: agreeing}
	s := newLLMScheduler(llmLimits{Workers: 1})
	cost := estimate("x0", "")
	s.addProvider("a", a, llmLimits{TPM: cost * 2})
	s.addProvider("b", b, llmLimits{TPM: cost * 2})
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		s.submit(&llmJob{prio: prioConfirm, text: fmt.Sprintf("x%d", i), deadline: time.Now().Add(time.Minute),
			done: func(LLMResult, error) { wg.Done() }})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reads waited for budget with a second provider free")
	}
	if a.n() != 2 || b.n() != 2 {
		t.Fatalf("reads a=%d b=%d, want 2 each", a.n(), b.n())
	}
	if st := s.stats(); st.Providers != 2 || st.Budget < 0 {
		t.Fatalf("stats %+v", st)
	}
}
