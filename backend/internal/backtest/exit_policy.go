package backtest

import (
	"time"

	"trading-systemv1/internal/exitpolicy"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/optionmath"
	"trading-systemv1/internal/strategy"
)

// btPolicyKey is the arbiter key of the replay's single open trade.
const btPolicyKey = "BT"

// btPolicy runs the live exit policy (internal/exitpolicy) over a replay
// on modeled premiums: greeks from the option model at entry, one
// evaluation per 1m bar. nil when off.
type btPolicy struct {
	arb  *exitpolicy.Arbiter
	plan exitpolicy.Plan
}

// policyExiter closes the strategy's position for an outside reason, as
// stratengine does for an act decision.
type policyExiter interface {
	ExitRequested(side strategy.PositionSide, fnoToken, reason string) *strategy.Signal
}

func (e *Engine) newBTPolicy() *btPolicy {
	sc := e.cfg.ExitPolicy
	if sc == nil || sc.Mode == exitpolicy.ModeOff || sc.Mode == "" || !e.cfg.Option.Enabled {
		return nil
	}
	return &btPolicy{arb: exitpolicy.NewArbiter(), plan: sc.Plan()}
}

// open tracks a trade entered on c (live tracks from the bar's close).
func (p *btPolicy) open(e *Engine, t *Trade, c model.TFCandle, targetMove int64) {
	if p == nil || t == nil || t.modelMid <= 0 {
		return
	}
	g := e.modelGreeks(t, c)
	p.arb.Open(exitpolicy.Position{
		Key: btPolicyKey, Strategy: "BACKTEST", Side: string(t.Side),
		IndexToken: "INDEX", FNOToken: "MODEL", EntryTS: c.TS.Add(time.Minute),
		IndexEntry: c.Close, PremEntry: t.modelMid,
		Greeks: exitpolicy.NewEntryGreeks(g.Delta, g.Theta, targetMove),
	}, p.plan)
}

// step feeds bar c and evaluates at its close.
func (p *btPolicy) step(e *Engine, strat backtestStrategy, t *Trade, c model.TFCandle) (exitpolicy.Decision, bool) {
	if p == nil {
		return exitpolicy.Decision{}, false
	}
	p.arb.OnTick("INDEX", c.Close)
	p.arb.OnTick("MODEL", e.modelMidAt(t, c))
	armed := func(pos exitpolicy.Position) bool {
		a, ok := strat.(interface {
			ProtectArmed(strategy.PositionSide) bool
		})
		return ok && a.ProtectArmed(strategy.PositionSide(pos.Side))
	}
	ds := p.arb.OnMinute(c.TS.Add(time.Minute), armed)
	if len(ds) == 0 {
		return exitpolicy.Decision{}, false
	}
	return ds[0], true
}

// modelGreeks are the option model's greeks for t's contract at c (theta
// in rupees per calendar day, as the live picker reports).
func (e *Engine) modelGreeks(t *Trade, c model.TFCandle) optionmath.Greeks {
	m := e.cfg.Option
	iv := e.vixAt(c.TS)
	if iv <= 0 {
		iv = m.IVPct
	}
	years := e.expiryFor(t, c.TS).Sub(c.TS).Hours() / (24 * 365)
	return optionmath.GreeksAt(float64(c.Close)/100, float64(t.Strike), years, iv/100, m.RatePct/100, t.Side == strategy.SideCall)
}
