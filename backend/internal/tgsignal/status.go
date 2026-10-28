package tgsignal

import (
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// statusTracker keeps the service's health for the dashboard. Daily
// counters reset at local midnight.
type statusTracker struct {
	mu    sync.Mutex
	st    model.TGStatus
	day   string
	sched *llmScheduler // nil = no LLM queue to show
}

func newStatusTracker(cfg Config) *statusTracker {
	t := &statusTracker{}
	t.st.Telegram.Chats = append([]int64(nil), cfg.ChatIDs...)
	t.st.LLM.Enabled = cfg.LLM
	if cfg.LLM {
		t.st.LLM.Model = cfg.LLMModel
		t.st.LLM.Provider = hostOf(cfg.LLMBaseURL)
	}
	t.st.Verdicts = map[string]int{}
	return t
}

// rollDay resets daily counters on a new day. The caller holds t.mu.
func (t *statusTracker) rollDay(now time.Time) {
	if d := now.Format("2006-01-02"); d != t.day {
		t.day = d
		t.st.Verdicts = map[string]int{}
		t.st.LLM.ReadsToday, t.st.LLM.ErrorsToday, t.st.LLM.TokensToday, t.st.LLM.AvgTokens = 0, 0, 0, 0
	}
}

func (t *statusTracker) telegramUp(user string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.st.Telegram.Connected, t.st.Telegram.User, t.st.Telegram.Since, t.st.Telegram.LastError = true, user, now, ""
}

func (t *statusTracker) telegramDown(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.st.Telegram.Connected = false
	if err != nil {
		t.st.Telegram.LastError = err.Error()
	}
}

func (t *statusTracker) message(now time.Time) {
	t.mu.Lock()
	t.st.Telegram.LastMessageAt = now
	t.mu.Unlock()
}

func (t *statusTracker) verdict(v string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollDay(now)
	t.st.Verdicts[v]++
}

func (t *statusTracker) llmRead(tokens int, err error, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollDay(now)
	t.st.LLM.LastReadAt = now
	if err != nil {
		t.st.LLM.OK, t.st.LLM.LastError = false, err.Error()
		t.st.LLM.ErrorsToday++
		return
	}
	t.st.LLM.OK, t.st.LLM.LastError = true, ""
	t.st.LLM.ReadsToday++
	t.st.LLM.TokensToday += tokens
	t.st.LLM.AvgTokens = t.st.LLM.TokensToday / t.st.LLM.ReadsToday
}

// snapshot returns a copy for publishing.
func (t *statusTracker) snapshot(now time.Time) model.TGStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollDay(now)
	s := t.st
	s.UpdatedAt = now
	if t.sched != nil {
		q := t.sched.stats()
		s.LLM.Queue, s.LLM.Running, s.LLM.BudgetTokens, s.LLM.ProviderInUse = q.Queue, q.Running, q.Budget, q.Provider
		s.LLM.Providers = q.Providers
	}
	s.Telegram.Chats = append([]int64(nil), t.st.Telegram.Chats...)
	s.Verdicts = make(map[string]int, len(t.st.Verdicts))
	for k, v := range t.st.Verdicts {
		s.Verdicts[k] = v
	}
	return s
}
