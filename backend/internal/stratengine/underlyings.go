package stratengine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/internal/underlying"
)

// ════════════════════════════════════════════════════════════════════
//  SR on underlyings beyond NIFTY (UNDERLYINGS=NIFTY,SENSEX).
//
//  NIFTY keeps every existing path (svc.srStrategy, the ATM picker, the
//  NIFTY option picker, the shared chain cache). Each extra underlying gets
//  its own SR instance with point parameters scaled to its index, its own
//  strike picker on its own segment, chain cache and strike ladder. Its
//  entries pick a contract by the same greek rules (selectSRContract) and
//  never fall back to the NIFTY contracts.
// ════════════════════════════════════════════════════════════════════

// srExtra is the SR strategy and option plumbing of one extra underlying.
type srExtra struct {
	prof  underlying.Profile
	strat *strategy.Nifty50SR

	mu      sync.Mutex
	picker  *orderexec.StrikePicker
	greeks  greeksSource  // test hook; nil = picker
	res     extraResolver // test hook; nil = picker
	chain   []orderexec.OptionContract
	chainAt time.Time

	ladderBusy       atomic.Bool
	ladderCenter     atomic.Int64
	ladderSubscribed map[string]bool // touched only by the ladder goroutine

	noGreeksLogged atomic.Bool
}

// extraResolver resolves an extra underlying's contracts. Satisfied by
// *orderexec.StrikePicker.
type extraResolver interface {
	legResolver
	expiryResolver
	NextExpiry(now time.Time) time.Time
}

// resolverOf returns ex's contract resolver.
func (svc *Service) resolverOf(ex *srExtra) extraResolver {
	if ex.res != nil {
		return ex.res
	}
	return svc.extraPicker(ex)
}

// snapshotKey is the strategy's key in the Redis snapshot ("sensex_sr").
func (ex *srExtra) snapshotKey() string { return strings.ToLower(ex.prof.StrategyName) }

// srExtraConfig derives an extra underlying's SR config from NIFTY's:
// point parameters scaled to the index, then any STRAT_SR_<KEY>_* override.
func srExtraConfig(base strategy.Nifty50SRConfig, p underlying.Profile) strategy.Nifty50SRConfig {
	c := base.ScalePoints(p.Scale)
	c.IndexToken = p.IndexKey()
	c.NameOverride = p.StrategyName
	c.StrikeStep = p.StrikeStep
	c.VolumeToken, c.FNOCallToken, c.FNOPutToken = "", "", ""
	pre := "STRAT_SR_" + p.Key + "_"
	c.MaxDayLossPts = config.GetEnvInt64(pre+"MAX_DAY_LOSS_PAISE", c.MaxDayLossPts)
	c.DayRunPts = config.GetEnvInt64(pre+"DAY_RUN_PAISE", c.DayRunPts)
	c.SupportStopZonePts = config.GetEnvInt64(pre+"SUPPORT_STOP_ZONE_PAISE", c.SupportStopZonePts)
	c.TouchTolPts = config.GetEnvInt64(pre+"TOUCH_TOL_PAISE", c.TouchTolPts)
	c.StopMinPts = config.GetEnvInt64(pre+"STOP_MIN_PAISE", c.StopMinPts)
	return c
}

// buildSRExtras creates one SR instance per extra underlying from NIFTY's
// config. The strike step comes from the instrument master when it is
// loaded.
func (svc *Service) buildSRExtras(base strategy.Nifty50SRConfig) []*srExtra {
	svc.srExtras = nil
	now := time.Now()
	for _, p := range underlying.All()[1:] {
		if step := orderexec.GetInstrumentMaster().StrikeStep(p.OptionName, p.FNOExchange, now); step > 0 && step != p.StrikeStep {
			log.Printf("[stratengine] %s strike step %d from instrument master (profile said %d)", p.Key, step, p.StrikeStep)
			p.StrikeStep = step
		}
		c := srExtraConfig(base, p)
		ex := &srExtra{prof: p, strat: strategy.NewNifty50SR(svc.cfg.Qty, c)}
		ex.picker = orderexec.NewStrikePickerFor(nil, p)
		ex.strat.SetExpiry(ex.picker.NextExpiry(now))
		svc.srExtras = append(svc.srExtras, ex)
	}
	return svc.srExtras
}

// srExtraFor returns the extra underlying whose SR strategy is name.
func (svc *Service) srExtraFor(name string) *srExtra {
	for _, ex := range svc.srExtras {
		if ex.strat.Name() == name {
			return ex
		}
	}
	return nil
}

// srAll returns every SR instance, NIFTY first.
func (svc *Service) srAll() []*strategy.Nifty50SR {
	var out []*strategy.Nifty50SR
	if svc.srStrategy != nil {
		out = append(out, svc.srStrategy)
	}
	for _, ex := range svc.srExtras {
		out = append(out, ex.strat)
	}
	return out
}

// srByName returns the SR instance named name, or nil.
func (svc *Service) srByName(name string) *strategy.Nifty50SR {
	for _, sr := range svc.srAll() {
		if sr.Name() == name {
			return sr
		}
	}
	return nil
}

// qualifyFNOTokenFor qualifies an option token with the segment of the
// strategy's underlying ("BFO:1163095"), NIFTY's as before.
func (svc *Service) qualifyFNOTokenFor(strategyName, token string) string {
	if ex := svc.srExtraFor(strategyName); ex != nil && token != "" && !strings.Contains(token, ":") {
		return ex.prof.FNOExchange + ":" + strings.TrimSpace(token)
	}
	return svc.qualifyFNOToken(token)
}

// subscribeOn streams option tokens of an extra underlying's segment.
func (svc *Service) subscribeOn(ctx context.Context, ex *srExtra, tokens ...string) {
	if len(tokens) == 0 {
		return
	}
	if svc.subscribeHook != nil {
		svc.subscribeHook(tokens)
		return
	}
	if svc.redisWriter != nil {
		svc.publishSubscriptionMode(ctx, ex.prof.FNOExchType, svc.fnoSubscribeMode(), tokens...)
	}
}

// extraPicker returns ex's strike picker, rebuilt with a broker session
// once one exists (the NIFTY executor's, or the NIFTY strike picker's
// standalone session): OptionGreek needs one, master lookups do not.
func (svc *Service) extraPicker(ex *srExtra) *orderexec.StrikePicker {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.picker != nil && ex.picker.SmartConnect() != nil {
		return ex.picker
	}
	sc := svc.orderExecutor.GetSmartConnect()
	if sc == nil && svc.strikePicker != nil {
		sc = svc.strikePicker.SmartConnect()
	}
	if sc != nil || ex.picker == nil {
		ex.picker = orderexec.NewStrikePickerFor(sc, ex.prof)
	}
	return ex.picker
}

// extraChain returns ex's option chain with greeks, cached deltaChainTTL.
func (svc *Service) extraChain(ex *srExtra, now time.Time) ([]orderexec.OptionContract, error) {
	src := ex.greeks
	if src == nil {
		p := svc.extraPicker(ex)
		if p.SmartConnect() == nil {
			return nil, fmt.Errorf("no broker session for %s OptionGreek", ex.prof.OptionName)
		}
		src = p
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.chain != nil && now.Sub(ex.chainAt) < deltaChainTTL && !now.Before(ex.chainAt) {
		return ex.chain, nil
	}
	chain, err := src.LoadOptionChain(now)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("empty %s option chain", ex.prof.OptionName)
	}
	ex.chain, ex.chainAt = chain, now
	return chain, nil
}

// resolveExtraEntry turns an extra underlying's SR entry into the exact
// contract to buy. The greek rules pick the strike and expiry as for NIFTY.
// Without a chain (no session, or OptionGreek not covering the index) it
// takes the strategy's ATM strike on the nearest listed expiry and says so
// once. A contract with no premium yet is subscribed and the entry refused.
func (svc *Service) resolveExtraEntry(ctx context.Context, ex *srExtra, sig *strategy.Signal, now time.Time) error {
	picker := svc.resolverOf(ex)
	opt := optionTypeFor(sig.Side)
	strike, expiry := sig.Strike, time.Time{}

	if chain, err := svc.extraChain(ex, now); err == nil {
		lim := svc.srGreekLimits()
		lim.TargetMove = sig.TargetMove
		lim.ViewTargetMove = ex.prof.Scale(lim.ViewTargetMove)
		premium := func(c orderexec.OptionContract) float64 {
			if c.Premium > 0 {
				return c.Premium
			}
			info, err := picker.ResolveStrikeOn(c.Expiry, c.Strike, opt)
			if err != nil {
				return 0
			}
			return float64(svc.orderExecutor.GetLTP(info.Token)) / 100
		}
		c, err := selectSRContract(chain, opt, now, lim, premium)
		if err != nil {
			return err
		}
		log.Printf("[stratengine] %s strike %d%s (asked %d): delta=%.2f gamma=%.4f theta=%.2f premium=%.2f iv=%.1f",
			sig.StrategyName, c.Strike, c.OptionType, sig.Strike, c.Delta, c.Gamma, c.Theta, premium(c), normIV(c.IV))
		svc.noteEntryGreeks(sig.StrategyName, c.Delta, c.Theta)
		strike, expiry = c.Strike, c.Expiry
	} else if ex.noGreeksLogged.CompareAndSwap(false, true) {
		log.Printf("[stratengine] ⚠️  %s: no option greeks (%v) — entries take the ATM strike on the nearest expiry without greek checks", sig.StrategyName, err)
	}

	var info orderexec.StrikeInfo
	var err error
	if expiry.IsZero() {
		info, err = picker.ResolveStrike(now, strike, opt)
	} else {
		info, err = picker.ResolveStrikeOn(expiry, strike, opt)
	}
	if err != nil {
		return fmt.Errorf("strike %d%s not resolvable: %w", strike, opt, err)
	}
	if svc.orderExecutor.GetLTP(info.Token) <= 0 {
		svc.subscribeOn(ctx, ex, info.Token)
		return fmt.Errorf("no premium yet for %s (%s)", info.Symbol, info.Token)
	}
	sig.Strike = info.Strike
	sig.FNOToken, sig.FNOSymbol = info.Token, info.Symbol
	sig.Pick = "old"
	return nil
}

// extraIndexTick keeps each extra underlying's strike ladder subscribed
// around its index (premiums for the greek pick, like NIFTY's ladder).
func (svc *Service) extraIndexTick(ctx context.Context, tick model.Tick) {
	for _, ex := range svc.srExtras {
		if tick.Token != ex.prof.IndexToken || (tick.Exchange != "" && tick.Exchange != ex.prof.IndexExchange) {
			continue
		}
		svc.refreshExtraLadder(ctx, ex, tick.Price)
	}
}

func (svc *Service) refreshExtraLadder(ctx context.Context, ex *srExtra, spot int64) {
	if spot <= 0 {
		return
	}
	if c := ex.ladderCenter.Load(); c != 0 && absInt64(spot-c) < ex.prof.Scale(ladderRecenterPaise) {
		return
	}
	if !ex.ladderBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer ex.ladderBusy.Store(false)
		now := time.Now()
		picker := svc.resolverOf(ex)
		ex.strat.SetExpiry(picker.NextExpiry(now))
		tokens := ex.ladderTokens(picker, now, spot)
		svc.subscribeOn(ctx, ex, tokens...)
		ex.ladderCenter.Store(spot)
		if len(tokens) > 0 {
			log.Printf("[stratengine] 🪜 %s strike ladder around %d: subscribed %d new contracts",
				ex.prof.Key, nearestStrike(spot, ex.prof.StrikeStep), len(tokens))
		}
	}()
}

// ladderTokens resolves ATM±ladderStrikes CE/PE and returns tokens not
// subscribed yet.
func (ex *srExtra) ladderTokens(res legResolver, now time.Time, spot int64) []string {
	if ex.ladderSubscribed == nil {
		ex.ladderSubscribed = make(map[string]bool)
	}
	step := ex.prof.StrikeStep
	atm := nearestStrike(spot, step)
	var out []string
	failures := 0
	for i := -ladderStrikes; i <= ladderStrikes; i++ {
		strike := atm + int64(i)*step
		for _, opt := range [...]string{"CE", "PE"} {
			info, err := res.ResolveStrike(now, strike, opt)
			if err != nil || info.Token == "" {
				if failures++; failures >= 3 {
					log.Printf("[stratengine] 🪜 %s strike ladder stopped after %d lookup failures (last: %v)", ex.prof.Key, failures, err)
					return out
				}
				continue
			}
			failures = 0
			if ex.ladderSubscribed[info.Token] {
				continue
			}
			ex.ladderSubscribed[info.Token] = true
			out = append(out, info.Token)
		}
	}
	return out
}

// refreshExtraVolumeTokens points each extra SR instance's volume at its
// index's nearest future on its own segment.
func (svc *Service) refreshExtraVolumeTokens(ctx context.Context, now time.Time) {
	im := orderexec.GetInstrumentMaster()
	for _, ex := range svc.srExtras {
		fut, err := im.NearestFutureOn(ex.prof.OptionName, ex.prof.FNOExchange, now)
		if err != nil {
			log.Printf("[stratengine] %s volume: no future (%v) — volume checks inactive", ex.prof.Key, err)
			continue
		}
		ex.strat.SetVolumeToken(ex.prof.FNOExchange + ":" + fut.Token)
		svc.subscribeOn(ctx, ex, fut.Token)
		log.Printf("[stratengine] %s volume from %s (token=%s)", ex.prof.Key, fut.Symbol, fut.Token)
	}
}

// warmupExtras replays each extra index's recent 1m candles into its SR.
func warmupExtras(reader model.CandleReader, extras []*srExtra, days int, now time.Time) {
	if days <= 0 {
		days = 5
	}
	after := now.AddDate(0, 0, -days).Unix()
	for _, ex := range extras {
		candles, err := reader.ReadTFCandles(ex.prof.IndexExchange, ex.prof.IndexToken, 60, after)
		if err != nil {
			log.Printf("[stratengine] %s warmup read error: %v — cold start", ex.strat.Name(), err)
			continue
		}
		n := ex.strat.Warmup(candles)
		log.Printf("[stratengine] 🔥 %s warmed with %d/%d 1m candles (%d days)", ex.strat.Name(), n, len(candles), days)
	}
}
