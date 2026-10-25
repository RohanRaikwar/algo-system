// TELEGRAM_SIGNAL: option calls read from a Telegram group by the tgsignal
// service arrive on cmd:tgsignal. Each call's contract is resolved from the
// instrument master, streamed, and armed in the strategy, which buys when
// the premium reaches the call's trigger (see strategy.TelegramSignal).

package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/internal/underlying"
)

// tgSignalMaxAge drops calls that sat on the bus (stratengine down or
// slow); the tgsignal service already drops old Telegram messages.
const tgSignalMaxAge = 2 * time.Minute

// tgSignalLoop arms each call.
func (svc *Service) tgSignalLoop(ctx context.Context) {
	svc.tgSubscribe(ctx, model.TGSignalChannel, func(payload string) {
		var call model.TGSignal
		if err := json.Unmarshal([]byte(payload), &call); err != nil {
			log.Printf("[stratengine] tgsignal bad payload: %v", err)
			return
		}
		now := time.Now()
		res, err := svc.armTGSignal(ctx, call, now)
		if err != nil {
			log.Printf("[stratengine] 📵 telegram call %s refused: %v", call.Key(), err)
			svc.publishTGOutcome(ctx, call.Key(), "REFUSED", err.Error())
			svc.recordRefusedEntry(ctx, strategy.Signal{
				StrategyName: strategy.TelegramSignalName,
				Side:         call.Side(),
				Strike:       call.Strike,
				Reason:       call.Text,
			}, "telegram call: "+err.Error(), now)
			return
		}
		if res == strategy.TGSLRaised {
			svc.saveTGPosition(ctx)
		}
	})
}

// tgFollowUpLoop applies the caller's replies (exit, SL hit, SL cost).
func (svc *Service) tgFollowUpLoop(ctx context.Context) {
	svc.tgSubscribe(ctx, model.TGFollowUpChannel, func(payload string) {
		var f model.TGFollowUp
		if err := json.Unmarshal([]byte(payload), &f); err != nil {
			log.Printf("[stratengine] tgfollowup bad payload: %v", err)
			return
		}
		sig, res, err := svc.tgFollowUp(f, time.Now())
		key := f.CallKey()
		if key == "" {
			key = f.Key()
		}
		if err != nil {
			log.Printf("[stratengine] ↩️ telegram follow-up %s ignored: %v", f.Key(), err)
			svc.publishTGOutcome(ctx, key, "FOLLOW_UP_IGNORED", fmt.Sprintf("%s (%s): %v", f.Action, f.Key(), err))
			return
		}
		log.Printf("[stratengine] ↩️ telegram follow-up %s → %s %s", f.Key(), f.Action, res)
		if sig != nil {
			svc.tfEngine.SendSignal(*sig)
			return // handleSignal saves the position
		}
		svc.saveTGPosition(ctx)
	})
}

// tgFollowUp checks a follow-up's age and hands it to the strategy.
func (svc *Service) tgFollowUp(f model.TGFollowUp, now time.Time) (*strategy.Signal, string, error) {
	if !f.ReceivedAt.IsZero() && now.Sub(f.ReceivedAt) > tgSignalMaxAge {
		return nil, "", fmt.Errorf("stale follow-up from %s", f.ReceivedAt.Format(time.RFC3339))
	}
	return svc.tgStrategy.FollowUp(f)
}

// tgSubscribe runs handle for each message on channel, resubscribing if
// the subscription drops.
func (svc *Service) tgSubscribe(ctx context.Context, channel string, handle func(string)) {
	client := svc.redisWriter.Client()
	backoff := time.Second
	for ctx.Err() == nil {
		sub := client.Subscribe(ctx, channel)
		for msg := range sub.Channel() {
			backoff = time.Second
			handle(msg.Payload)
		}
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("[stratengine] ⚠️  %s subscription closed — resubscribing in %s", channel, backoff)
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

// armTGSignal resolves a call's contract, checks it against the market,
// streams it and arms it.
func (svc *Service) armTGSignal(ctx context.Context, call model.TGSignal, now time.Time) (string, error) {
	if err := call.Validate(); err != nil {
		return "", err
	}
	if !call.ReceivedAt.IsZero() && now.Sub(call.ReceivedAt) > tgSignalMaxAge {
		return "", fmt.Errorf("stale call from %s", call.ReceivedAt.Format(time.RFC3339))
	}
	if !markethours.IsMarketOpen(now) || svc.pastEODCutoff(now) {
		return "", fmt.Errorf("market closed")
	}
	prof, info, err := svc.tgContract(call, now)
	if err != nil {
		return "", err
	}
	if err := svc.tgMarketCheck(call, prof, info); err != nil {
		return "", err
	}
	if svc.subscribeHook != nil {
		svc.subscribeHook([]string{info.Token})
	} else if svc.redisWriter != nil {
		svc.publishSubscriptionMode(ctx, prof.FNOExchType, svc.fnoSubscribeMode(), info.Token)
	}
	arm := strategy.TGArm{
		Call:          call,
		IndexToken:    prof.IndexToken,
		IndexExchange: prof.IndexExchange,
		FNOExchange:   prof.FNOExchange,
		FNOToken:      info.Token,
		FNOSymbol:     info.Symbol,
		ArmedAt:       now,
	}
	res, err := svc.tgStrategy.Arm(arm)
	if err != nil {
		return res, err
	}
	detail := fmt.Sprintf("%s above %d SL %d (%s), waits %dm", info.Symbol, call.TriggerPaise, call.SLPaise, call.Source, svc.cfg.TGArmMin)
	if res != strategy.TGSLRaised { // the strategy reports its own SL moves
		svc.publishTGOutcome(ctx, call.Key(), res, detail)
	}
	log.Printf("[stratengine] 📨 telegram call %s %s: %s", call.Key(), res, detail)
	return res, nil
}

// tgMarketCheck refuses a call that does not match the market: a strike
// far from spot, or an entry far from the live premium. Either means the
// call was misread or names another contract. Unknown prices pass here;
// the strategy checks the first premium tick.
func (svc *Service) tgMarketCheck(call model.TGSignal, prof underlying.Profile, info instruments.StrikeInfo) error {
	if svc.orderExecutor == nil {
		return nil
	}
	if spot := svc.orderExecutor.GetLTP(prof.IndexToken); spot > 0 && svc.cfg.TGMaxStrikeGapPct > 0 {
		strike := call.Strike * 100
		gap := strike - spot
		if gap < 0 {
			gap = -gap
		}
		if gap*100 > spot*svc.cfg.TGMaxStrikeGapPct {
			return fmt.Errorf("strike %d is more than %d%% from %s spot %d", call.Strike, svc.cfg.TGMaxStrikeGapPct, prof.Key, spot/100)
		}
	}
	if ltp := svc.orderExecutor.GetLTP(info.Token); ltp > 0 && svc.cfg.TGMaxPriceGapPct > 0 {
		gap := ltp - call.TriggerPaise
		if gap < 0 {
			gap = -gap
		}
		if gap*100 > call.TriggerPaise*svc.cfg.TGMaxPriceGapPct {
			return fmt.Errorf("entry %d is more than %d%% from live premium %d", call.TriggerPaise, svc.cfg.TGMaxPriceGapPct, ltp)
		}
	}
	return nil
}

// publishTGOutcome reports what was done with a call to tgsignal's review
// log.
func (svc *Service) publishTGOutcome(ctx context.Context, callKey, event, detail string) {
	if svc.redisWriter == nil {
		return
	}
	b, err := json.Marshal(model.TGOutcome{CallKey: callKey, Event: event, Detail: detail, TS: time.Now()})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := svc.redisWriter.Client().Publish(ctx, model.TGOutcomeChannel, b).Err(); err != nil {
		log.Printf("[stratengine] tg outcome publish: %v", err)
	}
}

// publishTGOutcomeAsync is the strategy's event callback: it runs under the
// strategy lock, so it must not block.
func (svc *Service) publishTGOutcomeAsync(callKey, event, detail string) {
	go func() {
		ctx := context.Background()
		// A raised stop (ladder or follow-up) must survive a restart.
		if event == strategy.TGSLRaised {
			svc.saveTGPosition(ctx)
		}
		svc.publishTGOutcome(ctx, callKey, event, detail)
	}()
}

// tgContract finds the call's contract offline: on expiry day today's
// contract (callers trade the expiring weekly), else the nearest listed.
func (svc *Service) tgContract(call model.TGSignal, now time.Time) (underlying.Profile, instruments.StrikeInfo, error) {
	ist := now.In(markethours.IST)
	today := time.Date(ist.Year(), ist.Month(), ist.Day(), 0, 0, 0, 0, markethours.IST)
	if call.Underlying == underlying.Primary().Key {
		prof := underlying.Primary()
		if info, ok := svc.lookupOffline(now, today, call.Strike, call.OptionType); ok {
			return prof, info, nil
		}
		info, err := svc.resolveEntryContract(now, time.Time{}, call.Strike, call.OptionType)
		return prof, info, err
	}
	for _, ex := range svc.srExtras {
		if ex.prof.Key != call.Underlying {
			continue
		}
		if info, ok := svc.extraOffline(ex, now, today, call.Strike, call.OptionType); ok {
			return ex.prof, info, nil
		}
		if info, ok := svc.extraOffline(ex, now, time.Time{}, call.Strike, call.OptionType); ok {
			return ex.prof, info, nil
		}
		return ex.prof, instruments.StrikeInfo{}, fmt.Errorf("%s %d%s not in instrument master", call.Underlying, call.Strike, call.OptionType)
	}
	return underlying.Profile{}, instruments.StrikeInfo{}, fmt.Errorf("%s not configured (INDEX_TOKEN)", call.Underlying)
}

// saveTGPosition stores the strategy's open trade, or clears it. Called
// after every TELEGRAM_SIGNAL signal.
func (svc *Service) saveTGPosition(ctx context.Context) {
	if svc.tgStrategy == nil || svc.redisWriter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client := svc.redisWriter.Client()
	p := svc.tgStrategy.Position()
	if p == nil {
		if err := client.Del(ctx, model.TGPositionKey).Err(); err != nil {
			log.Printf("[stratengine] tgsignal clear position: %v", err)
		}
		return
	}
	b, err := json.Marshal(p)
	if err == nil {
		err = client.Set(ctx, model.TGPositionKey, b, 24*time.Hour).Err()
	}
	if err != nil {
		log.Printf("[stratengine] tgsignal save position: %v", err)
	}
}

// restoreTGPosition reinstates the saved trade when the executor still
// holds it, so its stop keeps working after a restart.
func (svc *Service) restoreTGPosition(ctx context.Context) {
	if svc.tgStrategy == nil || svc.redisWriter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := svc.redisWriter.Client().Get(ctx, model.TGPositionKey).Result()
	if err != nil {
		return
	}
	var p strategy.TGPosition
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		log.Printf("[stratengine] tgsignal bad saved position: %v", err)
		return
	}
	rec, ok := svc.orderExecutor.GetEntryOrder(strategy.TelegramSignalName, p.Arm.Call.Side())
	if !ok || svc.unqualifyToken(rec.Token) != p.Arm.FNOToken {
		log.Printf("[stratengine] tgsignal saved position %s no longer held — dropped", p.Arm.FNOSymbol)
		svc.saveTGPosition(ctx)
		return
	}
	svc.tgStrategy.Restore(p)
	svc.publishSubscriptionMode(ctx, tgExchType(p.Arm.FNOExchange), svc.fnoSubscribeMode(), p.Arm.FNOToken)
	log.Printf("[stratengine] tgsignal restored %s entry=%d sl=%d target=%d", p.Arm.FNOSymbol, p.Entry, p.SL, p.Target)
}

// tgExchType is the Angel WS exchange type of an option segment.
func tgExchType(fnoExchange string) int {
	if p, ok := underlying.ByFNOExchange(fnoExchange); ok {
		return p.FNOExchType
	}
	return underlying.Primary().FNOExchType
}
