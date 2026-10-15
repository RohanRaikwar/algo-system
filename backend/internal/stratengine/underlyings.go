package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/internal/underlying"
)

// ════════════════════════════════════════════════════════════════════
//  SR on underlyings beyond NIFTY (INDEX_TOKEN=NSE:99926000,BSE:99919000).
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

	optPicker *optionpicker.Picker // STRAT_PICKER_MODE shadow/on; nil when off

	sel strikeSelState // strike selection view, as NIFTY's svc.strikeSel
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
	return svc.extraChainFresh(ex, now, deltaChainTTL)
}

// extraChainFresh is extraChain with an explicit TTL (the picker refreshes
// every 15s and shares this cache, as NIFTY's picker shares optionChain).
func (svc *Service) extraChainFresh(ex *srExtra, now time.Time, ttl time.Duration) ([]orderexec.OptionContract, error) {
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
	if ex.chain != nil && now.Sub(ex.chainAt) < ttl && !now.Before(ex.chainAt) {
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
	// STRAT_PICKER_MODE=on: the picker's contract or a refusal, as for NIFTY.
	if decided, err := svc.pickExtraEntry(ex, sig, now); decided {
		if err == nil {
			sig.Pick = "new"
		}
		return err
	}
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
		svc.recordExtraPick(ex, *sig, c, premium(c), now)
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
	svc.recordOldChoice(sig.StrategyName, sig.FNOSymbol)
	sig.Pick, sig.PickAlt = "old", svc.altPick(sig.StrategyName, sig.FNOSymbol, now)
	return nil
}

// ── Option picker per extra underlying ──

// buildExtraPickers gives each extra underlying its own option picker
// (spot from its index, chain from its OptionGreek, contracts on its
// segment) when STRAT_PICKER_MODE is shadow or on.
func (svc *Service) buildExtraPickers() {
	if !svc.pickerEnabled() || !svc.cfg.SREnabled || svc.orderExecutor == nil {
		return
	}
	for _, ex := range svc.srExtras {
		cfg := svc.pickerConfig()
		cfg.SpotToken, cfg.StrikeStep = ex.prof.IndexToken, ex.prof.StrikeStep
		ex.optPicker = optionpicker.New(cfg, extraChainAdapter{svc, ex}, extraResolverAdapter{svc, ex}, extraSubscriber{svc, ex})
		log.Printf("[stratengine] 🎯 %s option picker mode=%s", ex.prof.Key, svc.cfg.PickerMode)
	}
}

// runExtraPickers starts each extra picker's background refresh.
func (svc *Service) runExtraPickers(ctx context.Context) {
	for _, ex := range svc.srExtras {
		if ex.optPicker != nil {
			go ex.optPicker.Run(ctx, markethours.IsMarketOpen)
		}
	}
}

// pickExtraEntry is pickEntry for an extra underlying's SR entry.
func (svc *Service) pickExtraEntry(ex *srExtra, sig *strategy.Signal, now time.Time) (bool, error) {
	if ex.optPicker == nil || !svc.pickerEnabled() || sig.Action != strategy.ActionBuy {
		return false, nil
	}
	in := svc.srPickerIntent(optionTypeFor(sig.Side), sig.TargetMove)
	in.Strategy = ex.strat.Name()
	p, _, err := ex.optPicker.PickSingle(in, now)
	svc.recordPickerDecision(decisionFor(sig.StrategyName, svc.cfg.PickerMode, p, err, now))
	if svc.cfg.PickerMode != "on" {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("picker: %w", err)
	}
	sig.Strike, sig.FNOToken, sig.FNOSymbol = p.Strike, p.Token, p.Symbol
	svc.noteEntryGreeks(sig.StrategyName, p.Delta, p.Theta)
	return true, nil
}

type extraChainAdapter struct {
	svc *Service
	ex  *srExtra
}

func (a extraChainAdapter) Load(now time.Time) ([]optionpicker.Contract, error) {
	chain, err := a.svc.extraChainFresh(a.ex, now, 15*time.Second)
	if err != nil {
		return nil, err
	}
	out := make([]optionpicker.Contract, 0, len(chain))
	for _, c := range chain {
		out = append(out, optionpicker.Contract{
			Strike: c.Strike, Option: string(c.OptionType), Expiry: c.Expiry,
			IV: normIV(c.IV), Liquidity: c.LiquidityScore,
		})
	}
	return out, nil
}

// extraResolverAdapter looks contracts up through ex's current strike
// picker (rebuilt once a broker session exists).
type extraResolverAdapter struct {
	svc *Service
	ex  *srExtra
}

func (r extraResolverAdapter) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	info, ok := r.svc.extraPicker(r.ex).LookupOn(e, k, o)
	return info.Token, info.Symbol, ok
}

func (r extraResolverAdapter) Search(e time.Time, k int64, o string) (string, string, error) {
	info, err := r.svc.extraPicker(r.ex).ResolveStrikeOn(e, k, o)
	return info.Token, info.Symbol, err
}

// extraSubscriber streams the picker's universe on ex's segment.
type extraSubscriber struct {
	svc *Service
	ex  *srExtra
}

func (s extraSubscriber) SubscribeOptions(tokens []string) {
	if s.svc.redisWriter != nil {
		s.svc.publishSubscriptionMode(context.Background(), s.ex.prof.FNOExchType, smartSnapQuote, tokens...)
	}
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

// ── Strike selection view per extra underlying ──

// Extra underlyings' strike selection views, one full-state map by index
// key, so NIFTY's pub:strikesel stays as it was.
const (
	strikeSelXKey     = model.StrikeSelXKey
	strikeSelXChannel = model.StrikeSelXChannel
)

// extraLimits are the SR greek limits with the view target scaled to ex.
func (svc *Service) extraLimits(ex *srExtra) srGreekLimits {
	lim := svc.srGreekLimits()
	lim.ViewTargetMove = ex.prof.Scale(lim.ViewTargetMove)
	return lim
}

// extraPremium is srPremium on ex's contracts.
func (svc *Service) extraPremium(ex *srExtra, opt string) func(orderexec.OptionContract) float64 {
	res := svc.resolverOf(ex)
	return func(c orderexec.OptionContract) float64 {
		if c.Premium > 0 {
			return c.Premium
		}
		info, err := res.ResolveStrikeOn(c.Expiry, c.Strike, opt)
		if err != nil {
			return 0
		}
		return float64(svc.orderExecutor.GetLTP(info.Token)) / 100
	}
}

// extraContractView is contractView resolving on ex's segment.
func (svc *Service) extraContractView(ex *srExtra, c orderexec.OptionContract, premium float64, now time.Time) srContractView {
	v := srContractView{
		Strike: c.Strike, Option: string(c.OptionType), Symbol: c.Symbol, Token: c.Token,
		Expiry: dayStart(c.Expiry).Format("2006-01-02"), DTE: int(dayStart(c.Expiry).Sub(dayStart(now)).Hours() / 24),
		Delta: c.Delta, Gamma: c.Gamma, Theta: c.Theta, Vega: c.Vega, IV: normIV(c.IV),
		Premium: premium, Liquidity: c.LiquidityScore,
	}
	if v.Token == "" || v.Symbol == "" {
		if info, err := svc.resolverOf(ex).ResolveStrikeOn(c.Expiry, c.Strike, v.Option); err == nil {
			v.Token, v.Symbol = info.Token, info.Symbol
		}
	}
	return v
}

// recordExtraPick stores the contract bought for an extra SR entry.
func (svc *Service) recordExtraPick(ex *srExtra, sig strategy.Signal, c orderexec.OptionContract, premium float64, now time.Time) {
	last := &srLastPickView{
		srContractView: svc.extraContractView(ex, c, premium, now),
		Side:           string(sig.Side),
		AskedStrike:    sig.Strike,
		TS:             now.UTC().Format(time.RFC3339Nano),
	}
	ex.sel.mu.Lock()
	ex.sel.view.Last = last
	ex.sel.mu.Unlock()
}

// refreshExtraStrikeSels recomputes each extra underlying's live CE/PE pick,
// keeps its SR-expiry ladder subscribed and publishes all views.
func (svc *Service) refreshExtraStrikeSels(ctx context.Context, now time.Time) {
	if !svc.cfg.SREnabled || len(svc.srExtras) == 0 {
		return
	}
	for _, ex := range svc.srExtras {
		svc.refreshExtraStrikeSel(ctx, ex, now)
	}
	svc.publishExtraStrikeSels(ctx, now)
}

func (svc *Service) refreshExtraStrikeSel(ctx context.Context, ex *srExtra, now time.Time) {
	var call, put *srSideView
	errText := ""
	lim := svc.extraLimits(ex)
	if chain, err := svc.extraChain(ex, now); err != nil {
		errText = "no greeks: " + err.Error()
	} else {
		svc.subscribeExtraExpiryLadder(ctx, ex, chain, now)
		side := func(opt string) *srSideView {
			premium := svc.extraPremium(ex, opt)
			ev, err := evalSRContracts(chain, opt, now, lim, premium)
			if err != nil {
				return &srSideView{Error: err.Error()}
			}
			v := &srSideView{Rejects: ev.Rejects}
			if ev.Found {
				cv := svc.extraContractView(ex, ev.Best, premium(ev.Best), now)
				v.Pick = &cv
			}
			return v
		}
		call, put = side("CE"), side("PE")
	}
	if ex.optPicker != nil {
		for opt, s := range map[string]**srSideView{"CE": &call, "PE": &put} {
			if *s == nil {
				*s = &srSideView{}
			}
			in := svc.srPickerIntent(opt, lim.ViewTargetMove)
			in.Strategy = ex.strat.Name()
			p, rej, err := ex.optPicker.PickSingle(in, now)
			newPickerView(*s, p, rej, err, now)
		}
	}
	params := svc.srParamsView()
	params.ViewTargetPts = float64(lim.ViewTargetMove) / 100
	ex.sel.mu.Lock()
	v := &ex.sel.view
	v.Strategy, v.Params, v.UpdatedAt = ex.strat.Name(), params, now.UTC().Format(time.RFC3339)
	v.Call, v.Put, v.Error = call, put, errText
	v.Picker = nil
	if ex.optPicker != nil {
		st := ex.optPicker.Status(now)
		pv := &pickerView{Mode: svc.cfg.PickerMode, Streamed: st.Streamed, Spot: st.Spot, ChainError: st.ChainErr,
			Rules: pickerRulesView{MaxSpreadPct: svc.cfg.PickMaxSpreadPct, MaxQuoteAgeS: svc.cfg.PickMaxQuoteAge.Seconds(), MaxChainAgeS: svc.cfg.PickMaxChainAge.Seconds(), Rank: pickRank(svc.cfg.PickRank)}}
		if !st.ChainAt.IsZero() {
			pv.ChainAt = st.ChainAt.UTC().Format(time.RFC3339)
		}
		if d := svc.lastPickerDecision(ex.strat.Name()); d.Strategy != "" {
			pv.Decisions = []pickerDecision{d}
		}
		v.Picker = pv
	}
	ex.sel.mu.Unlock()
}

// subscribeExtraExpiryLadder is subscribeSRExpiryLadder for ex: when SR
// buys a later expiry than the nearest (SRMinDTE), its strikes around spot
// are kept streaming so they have premiums.
func (svc *Service) subscribeExtraExpiryLadder(ctx context.Context, ex *srExtra, chain []orderexec.OptionContract, now time.Time) {
	lim := svc.srGreekLimits()
	if lim.MinDTE <= 1 || svc.orderExecutor == nil {
		return
	}
	today := dayStart(now)
	var nearest, srExp time.Time
	for _, c := range chain {
		if c.Expiry.IsZero() {
			continue
		}
		d := daysBetween(today, c.Expiry)
		if d >= 1 && (nearest.IsZero() || c.Expiry.Before(nearest)) {
			nearest = c.Expiry
		}
		if d >= lim.MinDTE && (srExp.IsZero() || c.Expiry.Before(srExp)) {
			srExp = c.Expiry
		}
	}
	spot := svc.orderExecutor.GetLTP(ex.prof.IndexToken)
	if srExp.IsZero() || sameDay(srExp, nearest) || spot <= 0 {
		return
	}
	if ex.sel.ladderSubscribed == nil {
		ex.sel.ladderSubscribed = make(map[string]bool)
	}
	res := svc.resolverOf(ex)
	step := ex.prof.StrikeStep
	atm := nearestStrike(spot, step)
	var tokens []string
	failures := 0
	for i := -ladderStrikes; i <= ladderStrikes; i++ {
		for _, opt := range [...]string{"CE", "PE"} {
			info, err := res.ResolveStrikeOn(srExp, atm+int64(i)*step, opt)
			if err != nil || info.Token == "" {
				if failures++; failures >= 3 {
					svc.subscribeOn(ctx, ex, tokens...)
					return
				}
				continue
			}
			failures = 0
			if !ex.sel.ladderSubscribed[info.Token] {
				ex.sel.ladderSubscribed[info.Token] = true
				tokens = append(tokens, info.Token)
			}
		}
	}
	svc.subscribeOn(ctx, ex, tokens...)
}

// publishExtraStrikeSels publishes {"BSE:99919000": view, …}.
func (svc *Service) publishExtraStrikeSels(ctx context.Context, now time.Time) {
	views := make(map[string]strikeSelView, len(svc.srExtras))
	for _, ex := range svc.srExtras {
		ex.sel.mu.Lock()
		views[ex.prof.IndexKey()] = ex.sel.view
		ex.sel.mu.Unlock()
	}
	b, err := json.Marshal(views)
	if err != nil {
		log.Printf("[stratengine] strike selection X marshal error: %v", err)
		return
	}
	if svc.strikeSelXPublishHook != nil {
		svc.strikeSelXPublishHook(string(b))
		return
	}
	if svc.redisWriter == nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	svc.redisWriter.Client().Set(pctx, strikeSelXKey, b, 24*time.Hour)
	svc.redisWriter.Client().Publish(pctx, strikeSelXChannel, string(b))
}
