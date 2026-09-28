package stratengine

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  Per-entry option selection for the range strategies.
//
//  A range entry names its strike (ATM from the signal close, shifted OTM
//  by range class). It is resolved here and bought as that exact contract.
//  A strike ladder around spot is kept subscribed so every candidate
//  strike already has a premium when a signal fires. If the contract or
//  its premium is missing the entry is refused and the strategy's
//  position cancelled — never swapped for the 09:15 ATM pair.
// ════════════════════════════════════════════════════════════════════

const (
	ladderStrikes       = 8     // strikes each side of ATM (covers condor wings too)
	ladderRecenterPaise = 10000 // re-center once spot moves 100 pts
	ladderStrikeStep    = 50
)

// entryCanceller is a strategy that can drop an entry the engine refused.
type entryCanceller interface {
	CancelEntry(side strategy.PositionSide, reason string)
}

// positionTokenSetter learns the contract its position was entered in.
type positionTokenSetter interface {
	SetPositionToken(token string)
}

// ladderState is embedded in Service.
type ladderState struct {
	ladderBusy       atomic.Bool
	ladderCenter     atomic.Int64
	ladderSubscribed map[string]bool // touched only by the single ladder goroutine
	subscribeHook    func(tokens []string)
}

// nearestStrike rounds an index price (paise) to the nearest strike (points).
func nearestStrike(pricePaise, step int64) int64 {
	unit := step * 100
	return (pricePaise + unit/2) / unit * step
}

func optionTypeFor(side strategy.PositionSide) string {
	if side == strategy.SidePut {
		return "PE"
	}
	return "CE"
}

func (svc *Service) entryCancellerFor(name string) entryCanceller {
	if svc.nifty50RangeStrategy != nil && name == svc.nifty50RangeStrategy.Name() {
		return svc.nifty50RangeStrategy
	}
	return nil
}

func (svc *Service) positionTokenSetterFor(name string) positionTokenSetter {
	if svc.nifty50RangeStrategy != nil && name == svc.nifty50RangeStrategy.Name() {
		return svc.nifty50RangeStrategy
	}
	return nil
}

// resolveEntryStrike turns sig.Strike into the exact contract to buy.
// It refuses a contract with no premium yet (and subscribes it, so a
// later signal can use it).
func (svc *Service) resolveEntryStrike(ctx context.Context, sig *strategy.Signal, now time.Time) error {
	info, err := svc.resolverForLegs().ResolveStrike(now, sig.Strike, optionTypeFor(sig.Side))
	if err != nil {
		return fmt.Errorf("strike %d%s not resolvable: %w", sig.Strike, optionTypeFor(sig.Side), err)
	}
	if svc.cfg.RangeDeltaGuard {
		if info, err = svc.deltaChecked(sig, info, now); err != nil {
			return err
		}
	}
	if svc.orderExecutor.GetLTP(info.Token) <= 0 {
		svc.subscribeTokens(ctx, info.Token)
		return fmt.Errorf("no premium yet for %s (%s)", info.Symbol, info.Token)
	}
	if svc.cfg.RangeDeltaGuard {
		if err := svc.entryQualityCheck(sig, info, now); err != nil {
			return err
		}
	}
	sig.Strike = info.Strike
	sig.FNOToken, sig.FNOSymbol = info.Token, info.Symbol
	return nil
}

func (svc *Service) cancelStrategyEntry(sig strategy.Signal, reason string) {
	log.Printf("[stratengine] ENTRY REFUSED %s %s strike=%d: %s", sig.StrategyName, sig.Side, sig.Strike, reason)
	if c := svc.entryCancellerFor(sig.StrategyName); c != nil {
		c.CancelEntry(sig.Side, reason)
	}
}

func (svc *Service) subscribeTokens(ctx context.Context, tokens ...string) {
	if len(tokens) == 0 {
		return
	}
	if svc.subscribeHook != nil {
		svc.subscribeHook(tokens)
		return
	}
	if svc.redisWriter != nil {
		svc.publishFNOSubscription(ctx, tokens...)
	}
}

// refreshStrikeLadder keeps CE/PE for ATM±ladderStrikes subscribed around
// spot. Called on NIFTY ticks; resolution runs off the tick path, one at
// a time, and only after spot moved ladderRecenterPaise from the last center.
func (svc *Service) refreshStrikeLadder(ctx context.Context, spot int64) {
	if spot <= 0 {
		return
	}
	if c := svc.ladderCenter.Load(); c != 0 && absInt64(spot-c) < ladderRecenterPaise {
		return
	}
	if !svc.ladderBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer svc.ladderBusy.Store(false)
		tokens := svc.ladderTokens(time.Now(), spot)
		svc.subscribeTokens(ctx, tokens...)
		svc.ladderCenter.Store(spot)
		if len(tokens) > 0 {
			log.Printf("[stratengine] 🪜 strike ladder around %d: subscribed %d new contracts", nearestStrike(spot, ladderStrikeStep), len(tokens))
		}
	}()
}

// ladderTokens resolves the ladder and returns tokens not subscribed yet.
func (svc *Service) ladderTokens(now time.Time, spot int64) []string {
	if svc.ladderSubscribed == nil {
		svc.ladderSubscribed = make(map[string]bool)
	}
	res := svc.resolverForLegs()
	atm := nearestStrike(spot, ladderStrikeStep)
	var out []string
	failures := 0
	for i := -ladderStrikes; i <= ladderStrikes; i++ {
		strike := atm + int64(i)*ladderStrikeStep
		for _, opt := range [...]string{"CE", "PE"} {
			info, err := res.ResolveStrike(now, strike, opt)
			if err != nil || info.Token == "" {
				// Without the offline instrument master every miss is a
				// broker search; stop early instead of hammering the API.
				if failures++; failures >= 3 {
					log.Printf("[stratengine] 🪜 strike ladder stopped after %d lookup failures (last: %v)", failures, err)
					return out
				}
				continue
			}
			failures = 0
			if svc.ladderSubscribed[info.Token] {
				continue
			}
			svc.ladderSubscribed[info.Token] = true
			out = append(out, info.Token)
		}
	}
	return out
}

func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
