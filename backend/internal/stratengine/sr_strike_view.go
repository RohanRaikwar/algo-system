package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// NIFTY50_SR strike selection dashboard state. Same pattern as
// publishRangeState: the full view goes to a Redis key (REST and SNAPSHOT
// fallback) and a full-state channel (WS push). It shows the rules, the
// CE/PE the greeks would pick right now, and the last contract actually
// picked for an entry signal.
const (
	strikeSelKey     = "strikesel:state"
	strikeSelChannel = "pub:strikesel"
	strikeSelEvery   = 30 * time.Second
)

type srParamsView struct {
	DeltaMin        float64 `json:"delta_min"`
	DeltaMax        float64 `json:"delta_max"`
	ThetaMaxGainPct float64 `json:"theta_max_gain_pct"` // 0 = off
	ThetaHoldMin    float64 `json:"theta_hold_min"`
	ViewTargetPts   float64 `json:"view_target_pts"`
	MaxGamma        float64 `json:"max_gamma"` // 0 = off
	GammaDTE        int     `json:"gamma_dte"`
	MinLiquidity    float64 `json:"min_liquidity"` // 0 = off
	MaxBuyIV        float64 `json:"max_buy_iv"`    // %, 0 = off
	MinDTE          int     `json:"min_dte"`
	CostMultiple    int64   `json:"cost_multiple"` // 0 = off
}

// srContractView is one contract with its greeks. Premium is rupees, IV is %.
type srContractView struct {
	Strike    int64   `json:"strike"`
	Option    string  `json:"option"` // CE / PE
	Symbol    string  `json:"symbol,omitempty"`
	Token     string  `json:"token,omitempty"`
	Expiry    string  `json:"expiry"` // YYYY-MM-DD
	DTE       int     `json:"dte"`
	Delta     float64 `json:"delta"`
	Gamma     float64 `json:"gamma"`
	Theta     float64 `json:"theta"`
	Vega      float64 `json:"vega"`
	IV        float64 `json:"iv"`
	Premium   float64 `json:"premium"`
	Liquidity float64 `json:"liquidity"`
}

// srSideView is the live pick for one side; Pick is nil when nothing passes.
type srSideView struct {
	Pick    *srContractView `json:"pick,omitempty"`
	Rejects srRejects       `json:"rejects"`
	Error   string          `json:"error,omitempty"`
}

// srLastPickView is the contract bought for the last SR entry signal.
type srLastPickView struct {
	srContractView
	Side        string `json:"side"`
	AskedStrike int64  `json:"asked_strike"` // strategy's ATM before the greek pick
	TS          string `json:"ts"`
}

type strikeSelView struct {
	Strategy  string          `json:"strategy"`
	UpdatedAt string          `json:"updated_at"`
	Params    srParamsView    `json:"params"`
	Error     string          `json:"error,omitempty"` // option chain unavailable
	Call      *srSideView     `json:"call,omitempty"`
	Put       *srSideView     `json:"put,omitempty"`
	Last      *srLastPickView `json:"last,omitempty"`
	Picker    *pickerView     `json:"picker,omitempty"`
}

// pickerRulesView mirrors the picker's Rules that gate a contract, in units
// the dashboard can show directly (seconds, not time.Duration).
type pickerRulesView struct {
	MaxSpreadPct float64 `json:"max_spread_pct"`
	MaxQuoteAgeS float64 `json:"max_quote_age_s"`
	MaxChainAgeS float64 `json:"max_chain_age_s"`
	Rank         string  `json:"rank"`
}

// pickerView is the global option picker's status and last decision per
// strategy, from optionpicker.Picker.Status and svc.pickerDec.
type pickerView struct {
	Mode       string           `json:"mode"`
	ChainAt    string           `json:"chain_at,omitempty"`
	ChainError string           `json:"chain_error,omitempty"`
	Streamed   int              `json:"streamed"`
	Spot       int64            `json:"spot"`
	Rules      pickerRulesView  `json:"rules"`
	Decisions  []pickerDecision `json:"decisions"`
}

type strikeSelState struct {
	mu   sync.Mutex
	view strikeSelView

	// Tokens subscribed for the SR expiry ladder. Only refreshStrikeSel
	// (one goroutine) touches it.
	ladderSubscribed map[string]bool
}

func (svc *Service) srParamsView() srParamsView {
	l := svc.srGreekLimits()
	return srParamsView{
		DeltaMin: l.DeltaMin, DeltaMax: l.DeltaMax,
		ThetaMaxGainPct: l.ThetaMaxGainPct, ThetaHoldMin: l.ThetaHoldMin, ViewTargetPts: float64(l.ViewTargetMove) / 100,
		MaxGamma: l.MaxGamma, GammaDTE: l.GammaDTE, MinLiquidity: l.MinLiquidity,
		MaxBuyIV: l.MaxBuyIV, MinDTE: l.MinDTE, CostMultiple: l.CostMultiple,
	}
}

func (svc *Service) contractView(c orderexec.OptionContract, premium float64, now time.Time) srContractView {
	v := srContractView{
		Strike: c.Strike, Option: string(c.OptionType), Symbol: c.Symbol, Token: c.Token,
		Expiry: dayStart(c.Expiry).Format("2006-01-02"), DTE: int(dayStart(c.Expiry).Sub(dayStart(now)).Hours() / 24),
		Delta: c.Delta, Gamma: c.Gamma, Theta: c.Theta, Vega: c.Vega, IV: normIV(c.IV),
		Premium: premium, Liquidity: c.LiquidityScore,
	}
	if v.Token == "" || v.Symbol == "" {
		if info, err := svc.resolveOn(now, c.Expiry, c.Strike, v.Option); err == nil {
			v.Token, v.Symbol = info.Token, info.Symbol
		}
	}
	return v
}

// liveSRSide evaluates what the greeks would pick for opt right now.
func (svc *Service) liveSRSide(chain []orderexec.OptionContract, opt string, now time.Time) *srSideView {
	premium := svc.srPremium(now, opt)
	ev, err := evalSRContracts(chain, opt, now, svc.srGreekLimits(), premium)
	if err != nil {
		return &srSideView{Error: err.Error()}
	}
	side := &srSideView{Rejects: ev.Rejects}
	if ev.Found {
		v := svc.contractView(ev.Best, premium(ev.Best), now)
		side.Pick = &v
	}
	return side
}

// strikeSelLoop refreshes the live pick while the market is open.
func (svc *Service) strikeSelLoop(ctx context.Context) {
	t := time.NewTicker(strikeSelEvery)
	defer t.Stop()
	for {
		now := time.Now()
		if markethours.IsMarketOpen(now) {
			svc.refreshStrikeSel(ctx, now)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// subscribeSRExpiryLadder keeps ATM±ladderStrikes subscribed on the expiry
// SR buys when it is not the nearest one (SRMinDTE skips a next-day expiry):
// the main ladder only covers the nearest expiry, and without ticks those
// contracts have no premium and are rejected.
func (svc *Service) subscribeSRExpiryLadder(ctx context.Context, chain []orderexec.OptionContract, now time.Time) {
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
	spot := svc.orderExecutor.GetLTP("99926000") // NIFTY 50 index
	if srExp.IsZero() || sameDay(srExp, nearest) || spot <= 0 {
		return
	}
	st := &svc.strikeSel
	if st.ladderSubscribed == nil {
		st.ladderSubscribed = make(map[string]bool)
	}
	atm := nearestStrike(spot, ladderStrikeStep)
	var tokens []string
	failures := 0
	for i := -ladderStrikes; i <= ladderStrikes; i++ {
		for _, opt := range [...]string{"CE", "PE"} {
			info, err := svc.resolveOn(now, srExp, atm+int64(i)*ladderStrikeStep, opt)
			if err != nil || info.Token == "" {
				if failures++; failures >= 3 {
					log.Printf("[stratengine] 🪜 SR expiry ladder stopped after %d lookup failures (last: %v)", failures, err)
					svc.subscribeTokens(ctx, tokens...)
					return
				}
				continue
			}
			failures = 0
			if !st.ladderSubscribed[info.Token] {
				st.ladderSubscribed[info.Token] = true
				tokens = append(tokens, info.Token)
			}
		}
	}
	if len(tokens) > 0 {
		svc.subscribeTokens(ctx, tokens...)
		log.Printf("[stratengine] 🪜 SR expiry ladder %s around %d: subscribed %d contracts",
			srExp.Format("02Jan06"), atm, len(tokens))
	}
}

// refreshStrikeSel recomputes the live CE/PE pick and publishes the view.
// The chain fetch, SR expiry ladder and CE/PE picks are SR-only work: they
// run only when SR is enabled, so a picker-only deployment (SR disabled)
// never makes the broker chain call or the ladder subscriptions.
func (svc *Service) refreshStrikeSel(ctx context.Context, now time.Time) {
	var call, put *srSideView
	errText := ""
	if svc.cfg.SREnabled {
		if chain, err := svc.optionChain(now); err != nil {
			errText = "no greeks: " + err.Error()
		} else {
			svc.subscribeSRExpiryLadder(ctx, chain, now)
			call = svc.liveSRSide(chain, "CE", now)
			put = svc.liveSRSide(chain, "PE", now)
		}
	}
	svc.strikeSel.mu.Lock()
	v := &svc.strikeSel.view
	v.Call, v.Put, v.Error = call, put, errText
	b := svc.stampStrikeSel(now)
	svc.strikeSel.mu.Unlock()
	svc.publishStrikeSel(ctx, b)
}

// recordSRPick stores the contract bought for an SR entry and publishes it.
func (svc *Service) recordSRPick(sig strategy.Signal, c orderexec.OptionContract, premium float64, now time.Time) {
	last := &srLastPickView{
		srContractView: svc.contractView(c, premium, now),
		Side:           string(sig.Side),
		AskedStrike:    sig.Strike,
		TS:             now.UTC().Format(time.RFC3339Nano),
	}
	svc.strikeSel.mu.Lock()
	svc.strikeSel.view.Last = last
	b := svc.stampStrikeSel(now)
	svc.strikeSel.mu.Unlock()
	svc.publishStrikeSel(context.Background(), b)
}

// stampStrikeSel fills the fixed fields and marshals. Caller holds strikeSel.mu.
func (svc *Service) stampStrikeSel(now time.Time) []byte {
	v := &svc.strikeSel.view
	if svc.srStrategy != nil {
		v.Strategy = svc.srStrategy.Name()
	}
	v.Params = svc.srParamsView()
	v.UpdatedAt = now.UTC().Format(time.RFC3339)
	v.Picker = nil
	if svc.picker != nil {
		st := svc.picker.Status(now)
		pv := &pickerView{Mode: svc.cfg.PickerMode, Streamed: st.Streamed, Spot: st.Spot, ChainError: st.ChainErr,
			Rules: pickerRulesView{MaxSpreadPct: svc.cfg.PickMaxSpreadPct, MaxQuoteAgeS: svc.cfg.PickMaxQuoteAge.Seconds(), MaxChainAgeS: svc.cfg.PickMaxChainAge.Seconds(), Rank: pickRank(svc.cfg.PickRank)}}
		if !st.ChainAt.IsZero() {
			pv.ChainAt = st.ChainAt.UTC().Format(time.RFC3339)
		}
		svc.pickerDec.mu.Lock()
		for _, d := range svc.pickerDec.last {
			pv.Decisions = append(pv.Decisions, d)
		}
		svc.pickerDec.mu.Unlock()
		sort.Slice(pv.Decisions, func(i, j int) bool { return pv.Decisions[i].Strategy < pv.Decisions[j].Strategy })
		v.Picker = pv
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[stratengine] strike selection marshal error: %v", err)
		return nil
	}
	return b
}

func (svc *Service) publishStrikeSel(ctx context.Context, b []byte) {
	if b == nil {
		return
	}
	payload := string(b)
	if svc.strikeSelPublishHook != nil {
		svc.strikeSelPublishHook(payload)
		return
	}
	if svc.redisWriter == nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	svc.redisWriter.Client().Set(pctx, strikeSelKey, payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(pctx, strikeSelChannel, payload)
}
