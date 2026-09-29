package stratengine

import (
	"context"
	"encoding/json"
	"log"
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
	DeltaMin     float64 `json:"delta_min"`
	DeltaMax     float64 `json:"delta_max"`
	MaxThetaPct  float64 `json:"max_theta_pct"` // 0 = off
	MaxGamma     float64 `json:"max_gamma"`     // 0 = off
	GammaDTE     int     `json:"gamma_dte"`
	MinLiquidity float64 `json:"min_liquidity"` // 0 = off
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
}

type strikeSelState struct {
	mu   sync.Mutex
	view strikeSelView
}

func (svc *Service) srParamsView() srParamsView {
	l := svc.srGreekLimits()
	return srParamsView{
		DeltaMin: l.DeltaMin, DeltaMax: l.DeltaMax, MaxThetaPct: l.MaxThetaPct,
		MaxGamma: l.MaxGamma, GammaDTE: l.GammaDTE, MinLiquidity: l.MinLiquidity,
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
		if info, err := svc.resolverForLegs().ResolveStrike(now, c.Strike, v.Option); err == nil {
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

// refreshStrikeSel recomputes the live CE/PE pick and publishes the view.
func (svc *Service) refreshStrikeSel(ctx context.Context, now time.Time) {
	var call, put *srSideView
	errText := ""
	if chain, err := svc.optionChain(now); err != nil {
		errText = "no greeks: " + err.Error()
	} else {
		call = svc.liveSRSide(chain, "CE", now)
		put = svc.liveSRSide(chain, "PE", now)
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
