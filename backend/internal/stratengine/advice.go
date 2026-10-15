package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// publishAdvice journals and publishes entry advice (WATCH_BUY). It never
// reaches the order path: no dispatchOrder, no position maps, no P&L. The
// option shown is the contract SR's strike selection would pick, looked up
// without recording a pick (best effort: on any error the advice goes out
// with the index strike only).
func (svc *Service) publishAdvice(ctx context.Context, sig strategy.Signal, now time.Time) {
	var fnoPrice int64
	if sig.Strike > 0 && sig.FNOToken == "" {
		if err := svc.adviceContract(&sig, now); err != nil {
			log.Printf("[stratengine] ADVICE %s %s: no contract (%v)", sig.StrategyName, sig.Side, err)
		}
	}
	if sig.FNOToken != "" {
		fnoPrice = svc.orderExecutor.GetLTP(sig.FNOToken)
		if fnoPrice <= 0 {
			svc.subscribeTokens(ctx, sig.FNOToken)
		}
	}
	indexPrice := svc.orderExecutor.GetLTP(sig.Token)
	if indexPrice <= 0 {
		indexPrice = sig.Price
	}
	rec := sig
	rec.Price = fnoPrice
	rec.FNOToken = svc.unqualifyToken(sig.FNOToken)
	if err := svc.journal.Record(rec, now, nil, false, false); err != nil {
		log.Printf("[stratengine] advice journal write error: %v", err)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"strategy_name":     sig.StrategyName,
		"action":            sig.Action,
		"side":              sig.Side,
		"market_state":      sig.MarketState,
		"token":             sig.Token,
		"exchange":          sig.Exchange,
		"qty":               sig.Qty,
		"price":             fnoPrice,
		"index_price":       indexPrice,
		"current_fno_price": fnoPrice,
		"reason":            sig.Reason,
		"warnings":          sig.Warnings,
		"fno_token":         rec.FNOToken,
		"fno_symbol":        sig.FNOSymbol,
		"order_mode":        "ADVICE",
		"ts":                now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil || svc.redisWriter == nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := svc.redisWriter.Client().Publish(pctx, model.SignalChannel, string(payload)).Err(); err != nil {
		log.Printf("[stratengine] advice publish error: %v", err)
	}
	log.Printf("[stratengine] ADVICE (no order): %s %s %s fno=%s ltp=%d — %s",
		sig.StrategyName, sig.Side, sig.Token, sig.FNOSymbol, fnoPrice, sig.Reason)
}

// adviceContract fills the contract SR would buy for this signal. Unlike
// pickSRStrike it records no pick and no entry greeks: advice is not an
// entry.
func (svc *Service) adviceContract(sig *strategy.Signal, now time.Time) error {
	opt := optionTypeFor(sig.Side)
	var expiry time.Time
	strike := sig.Strike
	if svc.isSRSignal(sig) {
		if chain, err := svc.entryChain(now); err == nil {
			lim := svc.srGreekLimits()
			lim.TargetMove = sig.TargetMove
			if c, err := selectSRContract(chain, opt, now, lim, svc.srPremiumOffline(now, opt)); err == nil {
				strike, expiry = c.Strike, c.Expiry
			}
		}
	}
	info, err := svc.resolveEntryContract(now, expiry, strike, opt)
	if err != nil {
		return err
	}
	sig.Strike, sig.FNOToken, sig.FNOSymbol = info.Strike, info.Token, info.Symbol
	return nil
}
