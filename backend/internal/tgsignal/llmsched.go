package tgsignal

import (
	"container/list"
	"context"
	"errors"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/circuitbreaker"
)

// The LLM scheduler runs every LLM read of the service: one priority
// queue, a token budget per provider, failover to a second provider, and
// one request for identical prompts. Workers never sleep on a 429: the job
// goes back to the queue and the next provider takes it.

// llmPrio orders LLM reads, most urgent first.
type llmPrio int

const (
	prioExit    llmPrio = iota // a word message on a held call
	prioConfirm                // a word message on a waiting call
	prioVerify                 // the LLM check of a call armed on the regex
	prioNew                    // a possible call the regex cannot read
	prioAudit                  // the LLM's reading of a fast exit, log only
	prioNoise                  // other chat
	numPrios
)

var prioNames = [numPrios]string{"exit", "confirm", "verify", "new", "audit", "noise"}

func (p llmPrio) String() string { return prioNames[p] }

// ErrLLMStale is a read that did not start before its deadline.
var ErrLLMStale = errors.New("LLM read not started before its deadline")

// llmJob is one read. done is called once, from a scheduler goroutine; it
// must not block.
type llmJob struct {
	prio     llmPrio
	text     string
	ctxText  string
	deadline time.Time
	done     func(LLMResult, error)

	key     uint64
	cost    int
	tries   int
	waiters []func(LLMResult, error) // identical prompts read once
}

// Request timeout per attempt, and attempts per job across providers.
const (
	llmRequestTimeout = 20 * time.Second
	llmMaxTries       = 3
	llmCacheSize      = 512
)

// tokenBucket refills perSec up to cap. cap 0 = unlimited.
type tokenBucket struct {
	cap, level, perSec float64
	last               time.Time
}

func newBucket(perMinute int) tokenBucket {
	return tokenBucket{cap: float64(perMinute), level: float64(perMinute), perSec: float64(perMinute) / 60}
}

func (b *tokenBucket) refill(now time.Time) {
	if b.cap == 0 {
		return
	}
	if !b.last.IsZero() {
		b.level = min(b.cap, b.level+now.Sub(b.last).Seconds()*b.perSec)
	}
	b.last = now
}

// wait is how long until n fits, 0 if it fits now. A cost above the cap
// fits once the bucket is full.
func (b *tokenBucket) wait(n float64) time.Duration {
	if b.cap == 0 {
		return 0
	}
	n = min(n, b.cap)
	if b.level >= n {
		return 0
	}
	return time.Duration((n - b.level) / b.perSec * float64(time.Second))
}

func (b *tokenBucket) take(n float64) {
	if b.cap > 0 {
		b.level -= min(n, b.cap)
	}
}

// llmProvider is one API (primary or fallback) with its own budget.
type llmProvider struct {
	name    string
	reader  CallReader
	cb      *circuitbreaker.CircuitBreaker
	tokens  tokenBucket
	reqs    tokenBucket
	blocked time.Time // after a 429: not before this
}

// llmScheduler is the shared LLM queue.
type llmScheduler struct {
	mu        sync.Mutex
	queues    [numPrios][]*llmJob
	queued    map[uint64]*llmJob // queued or running, by prompt
	cache     map[uint64]*list.Element
	lru       *list.List
	providers []*llmProvider
	workers   int
	running   int
	wake      chan struct{}
	lastUsed  string
	cacheTTL  time.Duration // 0 = no cache
	now       func() time.Time
}

type cacheEntry struct {
	key uint64
	res LLMResult
	at  time.Time
}

// llmLimits size the scheduler. Zero TPM/RPM = no budget, zero CacheTTL
// = no cache.
type llmLimits struct {
	TPM, RPM int
	Workers  int
	CacheTTL time.Duration
}

func newLLMScheduler(lim llmLimits) *llmScheduler {
	if lim.Workers <= 0 {
		lim.Workers = 4
	}
	return &llmScheduler{
		queued: make(map[uint64]*llmJob), cache: make(map[uint64]*list.Element), lru: list.New(),
		workers: lim.Workers, cacheTTL: lim.CacheTTL, wake: make(chan struct{}, 1), now: time.Now,
	}
}

// addProvider adds an API. The first one added is tried first.
func (s *llmScheduler) addProvider(name string, r CallReader, lim llmLimits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers = append(s.providers, &llmProvider{
		name: name, reader: r, cb: circuitbreaker.NewCircuitBreaker(3, 30*time.Second),
		tokens: newBucket(lim.TPM), reqs: newBucket(lim.RPM),
	})
}

// estimate is a request's token cost before it is sent: about 4 bytes a
// token, plus the reply cap. The real count corrects the budget after.
func estimate(text, ctxText string) int {
	return (len(systemPromptFor(text, ctxText))+len(text)+len(ctxText))/4 + llmMaxTokens
}

func promptKey(text, ctxText string) uint64 {
	f := fnv.New64a()
	f.Write([]byte(text))
	f.Write([]byte{0})
	f.Write([]byte(ctxText))
	return f.Sum64()
}

// submit queues j. An identical prompt already queued or running takes j
// as a waiter; a fresh cached reading answers at once.
func (s *llmScheduler) submit(j *llmJob) {
	j.key, j.cost = promptKey(j.text, j.ctxText), estimate(j.text, j.ctxText)
	now := s.now()
	s.mu.Lock()
	if e, ok := s.cache[j.key]; ok {
		ce := e.Value.(*cacheEntry)
		if now.Sub(ce.at) < s.cacheTTL {
			s.lru.MoveToFront(e)
			res := ce.res
			res.Usage = Usage{}
			s.mu.Unlock()
			j.done(res, nil)
			return
		}
		s.lru.Remove(e)
		delete(s.cache, j.key)
	}
	if old, ok := s.queued[j.key]; ok {
		old.waiters = append(old.waiters, j.done)
		if j.deadline.After(old.deadline) {
			old.deadline = j.deadline
		}
		if j.prio < old.prio && s.unqueue(old) {
			old.prio = j.prio
			s.queues[old.prio] = append(s.queues[old.prio], old)
		}
		s.mu.Unlock()
		return
	}
	s.queued[j.key] = j
	s.queues[j.prio] = append(s.queues[j.prio], j)
	if s.running < s.workers {
		s.running++
		go s.work()
	}
	s.mu.Unlock()
	s.poke()
}

// unqueue removes a waiting job from its queue; false if it is running.
// The caller holds s.mu.
func (s *llmScheduler) unqueue(j *llmJob) bool {
	q := s.queues[j.prio]
	for i, x := range q {
		if x == j {
			s.queues[j.prio] = append(q[:i:i], q[i+1:]...)
			return true
		}
	}
	return false
}

func (s *llmScheduler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// work runs jobs until the queues are empty.
func (s *llmScheduler) work() {
	for {
		s.mu.Lock()
		j, p, wait, stale := s.next(s.now())
		if j == nil && wait == 0 {
			s.running--
			s.mu.Unlock()
			finishAll(stale, LLMResult{}, ErrLLMStale)
			return
		}
		s.mu.Unlock()
		finishAll(stale, LLMResult{}, ErrLLMStale)
		if j == nil {
			t := time.NewTimer(wait)
			select {
			case <-s.wake:
			case <-t.C:
			}
			t.Stop()
			continue
		}
		s.run(j, p)
	}
}

// next takes the most urgent job a provider can run now. Priority is
// strict: while the head job waits for budget, nothing below it runs.
// It returns the wait when a job is queued but cannot run yet, and the
// jobs past their deadline (removed). The caller holds s.mu.
func (s *llmScheduler) next(now time.Time) (*llmJob, *llmProvider, time.Duration, []*llmJob) {
	var stale []*llmJob
	for p := range s.queues {
		kept := s.queues[p][:0]
		for _, j := range s.queues[p] {
			if now.After(j.deadline) {
				delete(s.queued, j.key)
				stale = append(stale, j)
				continue
			}
			kept = append(kept, j)
		}
		s.queues[p] = kept
	}
	for p := range s.queues {
		if len(s.queues[p]) == 0 {
			continue
		}
		j := s.queues[p][0]
		wait := time.Duration(-1)
		for _, pr := range s.providers {
			if !pr.cb.Allow() {
				if wait < 0 || time.Second < wait {
					wait = time.Second
				}
				continue
			}
			pr.tokens.refill(now)
			pr.reqs.refill(now)
			w := max(pr.blocked.Sub(now), pr.tokens.wait(float64(j.cost)), pr.reqs.wait(1), 0)
			if w == 0 {
				pr.tokens.take(float64(j.cost))
				pr.reqs.take(1)
				s.queues[p] = s.queues[p][1:]
				return j, pr, 0, stale
			}
			if wait < 0 || w < wait {
				wait = w
			}
		}
		if wait <= 0 {
			wait = time.Second // no provider
		}
		return nil, nil, max(wait, 5*time.Millisecond), stale
	}
	return nil, nil, 0, stale
}

// run sends j to p and settles it: done, or back to the queue for another
// provider after a 429/5xx.
func (s *llmScheduler) run(j *llmJob, p *llmProvider) {
	var res LLMResult
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), llmRequestTimeout)
	_ = p.cb.Execute(func() error {
		res, err = p.reader.Read(ctx, j.text, j.ctxText)
		var re *rateError
		if errors.As(err, &re) {
			return err
		}
		return nil // a bad reply is not the provider's outage
	})
	cancel()
	now := s.now()

	s.mu.Lock()
	s.lastUsed = p.name
	var re *rateError
	switch {
	case err == nil:
		// Correct the estimate with what was used, then trust the
		// provider's own count.
		if res.Usage.Total > 0 {
			p.tokens.level += float64(j.cost - res.Usage.Total)
			p.tokens.level = min(p.tokens.level, p.tokens.cap)
		}
		if res.rate.ok && p.tokens.cap > 0 {
			p.tokens.level = min(p.tokens.level, float64(res.rate.remainingTokens))
		}
		s.store(j.key, res, now)
	case errors.As(err, &re):
		p.blocked = now.Add(re.wait)
		if re.rate.ok && re.rate.reset > re.wait {
			p.blocked = now.Add(re.rate.reset)
		}
		j.tries++
		if j.tries < llmMaxTries*len(s.providers) {
			log.Printf("[tgsignal] LLM %s busy (%v), %s read requeued", p.name, err, j.prio)
			s.queues[j.prio] = append([]*llmJob{j}, s.queues[j.prio]...)
			s.mu.Unlock()
			s.poke()
			return
		}
	}
	delete(s.queued, j.key)
	s.mu.Unlock()
	finishAll([]*llmJob{j}, res, err)
}

// store caches a reading. The caller holds s.mu.
func (s *llmScheduler) store(key uint64, res LLMResult, now time.Time) {
	if s.cacheTTL <= 0 {
		return
	}
	if e, ok := s.cache[key]; ok {
		s.lru.Remove(e)
	}
	s.cache[key] = s.lru.PushFront(&cacheEntry{key: key, res: res, at: now})
	for s.lru.Len() > llmCacheSize {
		e := s.lru.Back()
		s.lru.Remove(e)
		delete(s.cache, e.Value.(*cacheEntry).key)
	}
}

func finishAll(jobs []*llmJob, res LLMResult, err error) {
	for _, j := range jobs {
		j.done(res, err)
		for _, w := range j.waiters {
			r := res
			r.Usage = Usage{} // counted once
			w(r, err)
		}
	}
}

// llmQueueStats is the scheduler's state for the dashboard.
type llmQueueStats struct {
	Queue     map[string]int
	Running   int
	Budget    int // tokens all providers have now, -1 = no budget
	Provider  string
	Providers int
}

func (s *llmScheduler) stats() llmQueueStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := llmQueueStats{Queue: map[string]int{}, Running: s.running, Budget: -1, Provider: s.lastUsed, Providers: len(s.providers)}
	for p, q := range s.queues {
		if len(q) > 0 {
			st.Queue[prioNames[p]] = len(q)
		}
	}
	now := s.now()
	for _, p := range s.providers {
		if p.tokens.cap == 0 {
			continue
		}
		p.tokens.refill(now)
		st.Budget = max(st.Budget, 0) + int(max(p.tokens.level, 0))
	}
	return st
}
