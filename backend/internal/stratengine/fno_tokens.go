// Option token helpers: exchange-qualified tokens, symbols, the contract
// held by a restored position, and mdengine subscriptions for contracts.

package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"
)

type trackedInstrument struct {
	token    string
	exchange string
}

func (svc *Service) qualifyFNOToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.Contains(token, ":") {
		return token
	}
	exchange := strings.TrimSpace(svc.cfg.FNOExchange)
	if exchange == "" {
		exchange = "NFO"
	}
	return exchange + ":" + token
}

func (svc *Service) syncStrategyFNOTokens(callToken, putToken string) {
	callToken = svc.qualifyFNOToken(callToken)
	putToken = svc.qualifyFNOToken(putToken)

	if svc.srStrategy != nil {
		svc.srStrategy.SetFNOTokens(callToken, putToken)
	}
}

func (svc *Service) setStrategyFNOEntryPrice(strategyName string, price int64) {
	if price <= 0 {
		return
	}
	if sr := svc.srByName(strategyName); sr != nil {
		sr.SetFNOEntryPrice(price)
	}
}

func (svc *Service) normalizeExitInstrument(sig *strategy.Signal, entries map[string]trackedInstrument, posKey string) {
	if sig == nil {
		return
	}
	entry, ok := entries[posKey]
	if !ok || entry.token == "" || entry.exchange == "" {
		return
	}
	sig.Token = entry.token
	sig.Exchange = entry.exchange
}

// fnoSymbolFor names an option token for display ("NIFTY06OCT2622500PE"),
// from the instrument master; "" when unknown. Display only — the order path
// resolves its own instrument.
func (svc *Service) fnoSymbolFor(token string) string {
	exch := svc.cfg.FNOExchange
	if exch == "" {
		exch = "NFO"
	}
	im := instruments.GetInstrumentMaster()
	if name := im.SymbolForToken(exch, svc.unqualifyToken(token)); name != "" {
		return name
	}
	for _, ex := range svc.srExtras {
		if name := im.SymbolForToken(ex.prof.FNOExchange, svc.unqualifyToken(token)); name != "" {
			return name
		}
	}
	return ""
}

func (svc *Service) unqualifyToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if idx := strings.IndexByte(token, ':'); idx >= 0 && idx < len(token)-1 {
		return token[idx+1:]
	}
	return token
}

// restoredEntryFNO returns the contract and fill price of an open position
// from the executor's entry record. signalLoop's own entry maps start empty
// after a restart; this keeps an exit pinned to the contract actually held
// instead of the current ATM.
func (svc *Service) restoredEntryFNO(posKey string) (token string, price int64, ok bool) {
	if svc.orderExecutor == nil {
		return "", 0, false
	}
	rec, ok := svc.orderExecutor.GetEntryOrders()[posKey]
	if !ok || rec.Token == "" {
		return "", 0, false
	}
	return rec.Token, rec.Price, true
}

// subscribeHeldContracts streams every restored open position's option
// again. After a restart only the ATM pair and ladder get subscribed, so a
// position bought at another strike would get no ticks: no premium stops
// and a stale exit price.
func (svc *Service) subscribeHeldContracts(ctx context.Context) {
	if svc.orderExecutor == nil {
		return
	}
	var tokens []string
	for _, rec := range svc.orderExecutor.GetEntryOrders() {
		if rec.Token != "" {
			tokens = append(tokens, rec.Token)
		}
	}
	if len(tokens) == 0 {
		return
	}
	log.Printf("[stratengine] 📡 resubscribing %d held option contract(s) after restore: %v", len(tokens), tokens)
	svc.subscribeTokens(ctx, tokens...)
}

// publishFNOSubscription subscribes option tokens; SnapQuote when the picker
// runs (every option needs bid/ask), else Quote as before.
func (svc *Service) publishFNOSubscription(ctx context.Context, tokens ...string) {
	svc.publishFNOSubscriptionMode(ctx, svc.fnoSubscribeMode(), tokens...)
}

// fnoSubscribeMode is the feed mode publishFNOSubscription uses: SnapQuote
// while the global option picker is running (every option needs a two-sided
// quote), else Quote as before. A small, Redis-free seam so the mode
// selection itself is unit-tested without a broker or Redis.
func (svc *Service) fnoSubscribeMode() int {
	if svc.picker != nil {
		return smartSnapQuote
	}
	return smartconnect.ModeQuote
}

// publishFNOSubscriptionMode asks mdengine to stream NFO tokens in a feed
// mode (smartconnect.ModeQuote / ModeSnapQuote).
func (svc *Service) publishFNOSubscriptionMode(ctx context.Context, mode int, tokens ...string) {
	svc.publishSubscriptionMode(ctx, 2, mode, tokens...)
}

// publishSubscriptionMode asks mdengine to stream tokens of one Angel
// exchange_type (2 NFO, 4 BFO) in a feed mode.
func (svc *Service) publishSubscriptionMode(ctx context.Context, exchType, mode int, tokens ...string) {
	uniq := make([]string, 0, len(tokens))
	seen := make(map[string]struct{})
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		uniq = append(uniq, token)
	}
	if len(uniq) == 0 {
		return
	}

	subCmd, _ := json.Marshal(map[string]interface{}{
		"exchange_type": exchType,
		"tokens":        uniq,
		"mode":          mode,
	})
	if err := svc.redisWriter.Client().Publish(ctx, model.SubscribeTokenChannel, string(subCmd)).Err(); err != nil {
		log.Printf("[stratengine] ⚠️  failed to publish FNO subscribe command: %v", err)
		return
	}
	log.Printf("[stratengine] 📡 published FNO subscribe command: mode=%d tokens=%v", mode, uniq)
}
