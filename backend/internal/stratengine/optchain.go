package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/store/sqlite"
	"trading-systemv1/internal/underlying"
)

// ════════════════════════════════════════════════════════════════════
//  Option chain contract list (dashboard). For every underlying it lists
//  the nearest listed expiry's contracts around ATM from the instrument
//  master and writes them to optchain:contracts. It subscribes nothing and
//  calls no broker: prices are added by the gateway when a viewer asks,
//  and a contract streams only while a chart of it is open (option lease).
// ════════════════════════════════════════════════════════════════════

const optChainEvery = time.Minute

// optChainMaster is the part of the instrument master the chain reads.
type optChainMaster interface {
	OptionExpiriesOn(name, exchSeg string, from time.Time) []time.Time
	FindOption(name, exchSeg string, expiry time.Time, strike int64, optionType string) *instruments.Instrument
}

// optChainStrikes is OPTCHAIN_STRIKES (strikes each side of ATM, default 10).
func optChainStrikes() int {
	n := config.GetEnvInt("OPTCHAIN_STRIKES", 10)
	if n < 1 || n > 30 {
		return 10
	}
	return n
}

// nearestChainExpiry is the first listed expiry not over at now (today's
// counts until 15:30 IST).
func nearestChainExpiry(exps []time.Time, now time.Time) (time.Time, bool) {
	ist := now.In(markethours.IST)
	closed := ist.Hour() > 15 || (ist.Hour() == 15 && ist.Minute() >= 30)
	today := time.Date(ist.Year(), ist.Month(), ist.Day(), 0, 0, 0, 0, markethours.IST)
	for _, e := range exps {
		e = e.In(markethours.IST)
		if e.Before(today) || (sameDay(e, today) && closed) {
			continue
		}
		return e, true
	}
	return time.Time{}, false
}

// buildOptChain builds one index's view, or ok=false without spot or
// listed contracts. greeks is the cached chain (may be nil).
func buildOptChain(p underlying.Profile, im optChainMaster, spot int64, n int, greeks []instruments.OptionContract, now time.Time) (model.OptChainView, bool) {
	if spot <= 0 || p.StrikeStep <= 0 {
		return model.OptChainView{}, false
	}
	exp, ok := nearestChainExpiry(im.OptionExpiriesOn(p.OptionName, p.FNOExchange, now), now)
	if !ok {
		return model.OptChainView{}, false
	}
	stepPaise := p.StrikeStep * 100
	atm := (spot + stepPaise/2) / stepPaise * p.StrikeStep
	byToken := make(map[string]instruments.OptionContract, len(greeks))
	for _, c := range greeks {
		byToken[c.Token] = c
	}
	leg := func(strike int64, opt string) *model.OptChainLeg {
		inst := im.FindOption(p.OptionName, p.FNOExchange, exp, strike, opt)
		if inst == nil {
			return nil
		}
		l := &model.OptChainLeg{Token: inst.Token, Symbol: inst.Symbol}
		if g, ok := byToken[inst.Token]; ok {
			l.IV, l.Delta = normIV(g.IV), g.Delta
		}
		return l
	}
	v := model.OptChainView{
		Index: p.IndexKey(), Name: p.OptionName, Exchange: p.FNOExchange,
		Spot: spot, ATM: atm, Step: p.StrikeStep,
		Expiry: exp.Format("2006-01-02"), UpdatedAt: now.UTC().Format(time.RFC3339),
	}
	for i := -n; i <= n; i++ {
		k := atm + int64(i)*p.StrikeStep
		row := model.OptChainRow{Strike: k, CE: leg(k, "CE"), PE: leg(k, "PE")}
		if row.CE != nil || row.PE != nil {
			v.Rows = append(v.Rows, row)
		}
	}
	return v, len(v.Rows) > 0
}

// lastIndexClose is the newest stored 1m close of p's index within the
// last 5 days, or 0. It lets the chain list contracts outside market hours,
// when no index tick has arrived since start.
func lastIndexClose(reader model.CandleReader, p underlying.Profile, now time.Time) int64 {
	if reader == nil {
		return 0
	}
	cs, err := reader.ReadTFCandles(p.IndexExchange, p.IndexToken, 60, now.Add(-5*24*time.Hour).Unix())
	if err != nil || len(cs) == 0 {
		return 0
	}
	last := cs[0]
	for _, c := range cs {
		if c.TS.After(last.TS) {
			last = c
		}
	}
	return last.Close
}

// optChainViews builds every configured underlying's chain. Spot is the
// live index LTP, else closeOf (the last stored close); an index with
// neither keeps its previous view.
func (svc *Service) optChainViews(im optChainMaster, prev map[string]model.OptChainView, closeOf func(underlying.Profile) int64, now time.Time) map[string]model.OptChainView {
	n := optChainStrikes()
	out := make(map[string]model.OptChainView, 3)
	prim := underlying.Primary()
	var primGreeks []instruments.OptionContract
	if c, ok := svc.cachedChain(now, 10*time.Minute); ok {
		primGreeks = c
	}
	add := func(p underlying.Profile, greeks []instruments.OptionContract) {
		var spot int64
		if svc.orderExecutor != nil {
			spot = svc.orderExecutor.GetLTP(p.IndexToken)
		}
		if spot <= 0 && closeOf != nil {
			spot = closeOf(p)
		}
		if v, ok := buildOptChain(p, im, spot, n, greeks, now); ok {
			out[p.IndexKey()] = v
		} else if old, ok := prev[p.IndexKey()]; ok {
			out[p.IndexKey()] = old
		}
	}
	add(prim, primGreeks)
	// Every index in INDEX_TOKEN gets a chain, whether or not its SR
	// strategy runs (STRAT_SR_ENABLED=false leaves srExtras empty). Greeks
	// come from a running SR instance's cached chain when there is one.
	greeksOf := make(map[string][]instruments.OptionContract, len(svc.srExtras))
	for _, ex := range svc.srExtras {
		ex.mu.Lock()
		greeksOf[ex.prof.Key] = ex.chain
		ex.mu.Unlock()
	}
	for _, p := range underlying.All() {
		if p.Key == prim.Key {
			continue
		}
		add(p, greeksOf[p.Key])
	}
	return out
}

// optChainLoop writes optchain:contracts every minute (OPTCHAIN=off
// disables it).
func (svc *Service) optChainLoop(ctx context.Context) {
	if strings.EqualFold(config.GetEnv("OPTCHAIN", "on"), "off") || svc.redisWriter == nil {
		return
	}
	im := instruments.GetInstrumentMaster()
	var prev map[string]model.OptChainView
	var last string
	t := time.NewTicker(optChainEvery)
	defer t.Stop()
	for {
		if im.IsLoaded() {
			closeOf := func(p underlying.Profile) int64 {
				reader, err := sqlite.NewReader(svc.cfg.SQLitePath)
				if err != nil {
					return 0
				}
				defer reader.Close()
				return lastIndexClose(reader, p, time.Now())
			}
			views := svc.optChainViews(im, prev, closeOf, time.Now())
			if b, err := json.Marshal(views); err == nil && len(views) > 0 && string(b) != last {
				pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				if err := svc.redisWriter.Client().Set(pctx, model.OptChainContractsKey, b, 24*time.Hour).Err(); err != nil {
					log.Printf("[stratengine] ⚠️  optchain write: %v", err)
				} else {
					last, prev = string(b), views
				}
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
