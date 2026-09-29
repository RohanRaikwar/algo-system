package optionpicker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/model"
)

const SpotToken = "99926000"

type Config struct {
	Rules         Rules
	LadderStrikes int
	StrikeStep    int64
	ChainEvery    time.Duration
	ChainBackoff  time.Duration
	SearchEvery   time.Duration
}

type Status struct {
	ChainAt  time.Time
	ChainErr string
	Streamed int
	Spot     int64
}

// Picker chooses option contracts from memory: chain snapshot (background),
// universe tokens and live quotes. Pick* never perform I/O.
type Picker struct {
	cfg    Config
	chain  *ChainCache
	quotes *QuoteBook
	univ   *Universe
	spot   atomic.Int64

	mu      sync.Mutex
	lastDay string
}

func New(cfg Config, src ChainSource, res Resolver, sub Subscriber) *Picker {
	return &Picker{
		cfg:    cfg,
		chain:  NewChainCache(src, cfg.ChainEvery, cfg.ChainBackoff),
		quotes: NewQuoteBook(cfg.Rules.MaxQuoteAge),
		univ:   NewUniverse(res, sub, cfg.LadderStrikes, cfg.StrikeStep),
	}
}

// OnTick is on the tick path: an atomic store or one QuoteBook write.
func (p *Picker) OnTick(t model.Tick) {
	if t.Token == SpotToken {
		if t.Price > 0 {
			p.spot.Store(t.Price)
		}
		return
	}
	p.quotes.Update(t)
}

func (p *Picker) Quotes() *QuoteBook { return p.quotes }

func (p *Picker) Run(ctx context.Context, active func(time.Time) bool) {
	go p.chain.Run(ctx, active)
	go p.univ.RunSearch(ctx, p.cfg.SearchEvery)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshUniverseAt(time.Now())
		}
	}
}

// refreshUniverseAt re-centres the universe on spot over the chain's
// expiries. mdengine forgets dynamic subscriptions each trading session, so
// on a calendar-day change (IST) the universe re-subscribes its whole ladder
// before refreshing.
func (p *Picker) refreshUniverseAt(now time.Time) {
	day := dayKey(now)
	p.mu.Lock()
	newSession := p.lastDay != "" && p.lastDay != day
	p.lastDay = day
	p.mu.Unlock()
	if newSession {
		p.univ.NewSession()
	}

	contracts, _ := p.chain.Snapshot()
	seen := map[string]time.Time{}
	for _, c := range contracts {
		if !c.Expiry.IsZero() {
			seen[dayKey(c.Expiry)] = c.Expiry
		}
	}
	exps := make([]time.Time, 0, len(seen))
	for _, e := range seen {
		exps = append(exps, e)
	}
	p.univ.Refresh(p.spot.Load(), exps)
}

// candidates joins the chain snapshot with universe tokens.
func (p *Picker) candidates() ([]Contract, time.Time) {
	contracts, at := p.chain.Snapshot()
	out := make([]Contract, len(contracts))
	for i, c := range contracts {
		c.Token, c.Symbol, _ = p.univ.Token(c.Expiry, c.Strike, c.Option)
		out[i] = c
	}
	return out, at
}

func (p *Picker) env(now, chainAt time.Time) Env {
	return Env{Spot: p.spot.Load(), Now: now, ChainAt: chainAt, Quote: p.quotes.Get}
}

func (p *Picker) PickSingle(in SingleIntent, now time.Time) (Pick, Rejects, error) {
	c, at := p.candidates()
	return SelectSingle(c, in, p.cfg.Rules, p.env(now, at))
}

func (p *Picker) PickCondor(in CondorIntent, now time.Time) (CondorPick, Rejects, error) {
	c, at := p.candidates()
	return SelectCondor(c, in, p.cfg.Rules, p.env(now, at))
}

func (p *Picker) Status(now time.Time) Status {
	_, at := p.chain.Snapshot()
	st := Status{ChainAt: at, Streamed: p.univ.Size(), Spot: p.spot.Load()}
	if err := p.chain.LastError(); err != nil {
		st.ChainErr = err.Error()
	}
	return st
}

// RefreshForTest loads the chain and re-centres the universe synchronously.
func RefreshForTest(p *Picker, now time.Time) {
	p.chain.Refresh(now)
	p.refreshUniverseAt(now)
}
