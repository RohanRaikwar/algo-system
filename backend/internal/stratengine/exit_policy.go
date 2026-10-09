package stratengine

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/exitpolicy"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// exitPolicyReloadChannel re-reads STRAT_EXITPOLICY_FILE. The new config
// applies to positions opened afterwards; open positions keep their plan.
const exitPolicyReloadChannel = "cmd:exitpolicy:reload"

// exitPolicyOwner is the strategy a policy decision closes: it books and
// resets its own state and returns the exit signal.
type exitPolicyOwner interface {
	exitRequester
	ProtectArmed(side strategy.PositionSide) bool
}

// exitPolicyState is the unified exit layer (internal/exitpolicy) wired
// into stratengine: signalLoop opens and closes positions, tickRouterLoop
// feeds prices, exitPolicyLoop evaluates once a minute.
type exitPolicyState struct {
	arbiter  *exitpolicy.Arbiter
	recorder *exitpolicy.Recorder // nil = not recorded

	mu      sync.Mutex
	cfg     exitpolicy.Config
	pending map[string]pendingGreeks // strategy → pick greeks awaiting the entry

	sendHook   func(strategy.Signal)      // test hook; nil = tfEngine.SendSignal
	testOwners map[string]exitPolicyOwner // test hook; nil = live strategies
}

type pendingGreeks struct{ delta, theta float64 }

func (e *exitPolicyState) init(cfg exitpolicy.Config, rec *exitpolicy.Recorder) {
	e.arbiter = exitpolicy.NewArbiter()
	e.recorder = rec
	e.cfg = cfg
	e.pending = map[string]pendingGreeks{}
}

// initExitPolicy loads the policy file (defaults when it is missing) and
// opens the decision recorder.
func (svc *Service) initExitPolicy() {
	cfg, err := exitpolicy.LoadConfig(svc.cfg.ExitPolicyFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		log.Printf("[exitpolicy] %s missing — using defaults", svc.cfg.ExitPolicyFile)
	case err != nil:
		log.Printf("[exitpolicy] ⚠️  %v — using defaults", err)
		cfg = exitpolicy.DefaultConfig()
	}
	var rec *exitpolicy.Recorder
	if svc.cfg.ExitPolicyDB != "" {
		if rec, err = exitpolicy.OpenRecorder(svc.cfg.ExitPolicyDB); err != nil {
			log.Printf("[exitpolicy] ⚠️  recorder %s: %v — decisions not recorded", svc.cfg.ExitPolicyDB, err)
			rec = nil
		}
	}
	svc.exitPol.init(cfg, rec)
	for name, sc := range cfg.Strategies {
		log.Printf("[exitpolicy] %s mode=%s theta=%+v", name, sc.Mode, sc.Theta)
	}
}

// noteEntryGreeks keeps the greeks of the contract just picked for
// strategy's entry (theta in rupees per calendar day).
func (svc *Service) noteEntryGreeks(strategyName string, delta, theta float64) {
	e := &svc.exitPol
	if e.arbiter == nil {
		return
	}
	e.mu.Lock()
	e.pending[strategyName] = pendingGreeks{delta, theta}
	e.mu.Unlock()
}

// takeEntryGreeks returns and forgets the greeks noted for strategy's
// entry; nil when none were noted.
func (svc *Service) takeEntryGreeks(strategyName string, targetMove int64) *exitpolicy.EntryGreeks {
	e := &svc.exitPol
	if e.arbiter == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.pending[strategyName]
	if !ok {
		return nil
	}
	delete(e.pending, strategyName)
	return exitpolicy.NewEntryGreeks(g.delta, g.theta, targetMove)
}

// openExitPolicy starts tracking a single-leg entry under its strategy's
// plan. Unlisted strategies, legs and entries without a premium are left
// alone. Pending greeks are consumed either way so they never leak into a
// later entry.
func (svc *Service) openExitPolicy(sig strategy.Signal, posKey, fnoToken string, premEntry, indexEntry int64, now time.Time) {
	e := &svc.exitPol
	if e.arbiter == nil {
		return
	}
	greeks := svc.takeEntryGreeks(sig.StrategyName, sig.TargetMove)
	if sig.Leg != "" || premEntry <= 0 {
		return
	}
	e.mu.Lock()
	plan, ok := e.cfg.PlanFor(sig.StrategyName)
	e.mu.Unlock()
	if !ok {
		return
	}
	e.arbiter.Open(exitpolicy.Position{
		Key: posKey, Strategy: sig.StrategyName, Side: string(sig.Side),
		IndexToken: svc.unqualifyToken(sig.Token), FNOToken: svc.unqualifyToken(fnoToken),
		EntryTS: now, IndexEntry: indexEntry, PremEntry: premEntry, Greeks: greeks,
	}, plan)
	log.Printf("[exitpolicy] tracking %s (%s) prem=%d index=%d greeks=%v", posKey, plan.Mode, premEntry, indexEntry, greeks != nil)
}

// closeExitPolicy stops tracking a position that exited for any reason.
func (svc *Service) closeExitPolicy(posKey string) {
	if a := svc.exitPol.arbiter; a != nil {
		a.Close(posKey)
	}
}

func (svc *Service) exitPolicyTick(tick model.Tick) {
	if a := svc.exitPol.arbiter; a != nil {
		a.OnTick(tick.Token, tick.Price)
	}
}

func (svc *Service) exitPolicyOwnerFor(name string) exitPolicyOwner {
	if svc.exitPol.testOwners != nil {
		return svc.exitPol.testOwners[name]
	}
	if svc.srStrategy != nil && name == svc.srStrategy.Name() {
		return svc.srStrategy
	}
	return nil
}

// evaluateExitPolicy runs the minute evaluation and acts on its decisions:
// shadow ones are recorded, act ones close the position through its
// strategy (which books and resets) and the normal exit path.
func (svc *Service) evaluateExitPolicy(now time.Time) {
	e := &svc.exitPol
	armed := func(p exitpolicy.Position) bool {
		o := svc.exitPolicyOwnerFor(p.Strategy)
		return o != nil && o.ProtectArmed(strategy.PositionSide(p.Side))
	}
	for _, d := range e.arbiter.OnMinute(now, armed) {
		p := d.Position
		if d.Shadow || p.Strategy == model.RealOrderStrategy {
			log.Printf("[exitpolicy] 👻 would exit %s %s: %s", p.Strategy, p.Side, d.Text)
			svc.recordExitDecision(d, false, "shadow")
			continue
		}
		o := svc.exitPolicyOwnerFor(p.Strategy)
		if o == nil {
			svc.recordExitDecision(d, false, "strategy not running")
			continue
		}
		sig := o.ExitRequested(strategy.PositionSide(p.Side), p.FNOToken, d.Text)
		if sig == nil {
			svc.recordExitDecision(d, false, "no open position")
			continue
		}
		log.Printf("[exitpolicy] 🚪 closing %s %s: %s", p.Strategy, p.Side, d.Text)
		svc.recordExitDecision(d, true, "")
		if e.sendHook != nil {
			e.sendHook(*sig)
		} else {
			svc.tfEngine.SendSignal(*sig)
		}
	}
}

func (svc *Service) recordExitDecision(d exitpolicy.Decision, acted bool, note string) {
	if r := svc.exitPol.recorder; r != nil {
		r.Record(d, acted, note)
	}
}

// exitPolicyLoop evaluates shortly after each minute boundary, once the
// minute's last ticks are in.
func (svc *Service) exitPolicyLoop(ctx context.Context) {
	if r := svc.exitPol.recorder; r != nil {
		go r.Run(ctx.Done())
	}
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute + time.Second)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
			svc.evaluateExitPolicy(time.Now())
		}
	}
}

// reloadExitPolicy swaps the config for new positions; a bad file keeps
// the current one.
func (svc *Service) reloadExitPolicy() {
	cfg, err := exitpolicy.LoadConfig(svc.cfg.ExitPolicyFile)
	if err != nil {
		log.Printf("[exitpolicy] ⚠️  reload refused: %v", err)
		return
	}
	svc.exitPol.mu.Lock()
	svc.exitPol.cfg = cfg
	svc.exitPol.mu.Unlock()
	log.Printf("[exitpolicy] 🔄 reloaded %s (applies to new positions)", svc.cfg.ExitPolicyFile)
}

// exitPolicyReloadLoop follows cmd:exitpolicy:reload, resubscribing if the
// subscription drops.
func (svc *Service) exitPolicyReloadLoop(ctx context.Context) {
	client := svc.redisWriter.Client()
	backoff := time.Second
	for ctx.Err() == nil {
		sub := client.Subscribe(ctx, exitPolicyReloadChannel)
		for range sub.Channel() {
			backoff = time.Second
			svc.reloadExitPolicy()
		}
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("[stratengine] ⚠️  %s subscription closed — resubscribing in %s", exitPolicyReloadChannel, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
