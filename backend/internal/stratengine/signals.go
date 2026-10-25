// Signal handling: signalLoop takes each strategy signal through the entry
// gates and contract selection, books entries and exits, dispatches the
// order to its strategy's worker, then journals, publishes and notifies.

package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/notification"
	"trading-systemv1/internal/strategy"
)

// signalLoop reads signals from the TFEngine and writes to journal + notifier + PubSub.
func (svc *Service) signalLoop(ctx context.Context) {
	book := newSignalBook()
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-svc.tfEngine.Signals():
			if !ok {
				return
			}
			for _, s := range expandReverseSignals(sig) {
				svc.handleSignal(ctx, book, s, time.Now())
			}
		}
	}
}

// signalBook is signalLoop's memory of open entries, keyed
// "strategy|side", so an exit books and reports against the contract and
// regime of its entry. Touched only by the signalLoop goroutine.
type signalBook struct {
	// option LTP at entry (paise)
	fnoPrices map[string]int64
	// exact option token bought, so exit P&L stays pinned to it even after
	// dynamic strikes roll/reset
	fnoTokens map[string]string
	// market state at entry, so the exit keeps the same regime label
	marketStates map[string]string
	// strategy instrument at entry, so an exit triggered by an option tick
	// closes the same logical position
	instruments map[string]trackedInstrument
}

func newSignalBook() *signalBook {
	return &signalBook{
		fnoPrices:    make(map[string]int64),
		fnoTokens:    make(map[string]string),
		marketStates: make(map[string]string),
		instruments:  make(map[string]trackedInstrument),
	}
}

// signalOutcome is what handleSignal reports for one signal.
type signalOutcome struct {
	tradedToken     string // option traded: the entry's token for an exit
	currentFNOPrice int64
	stoplossPrice   int64
	orderMode       string // PAPER, PAPER_CAP or REAL
	liveMode        bool
	profitCapHit    bool
}

// handleSignal runs one signal: entry gates and contract selection,
// entry/exit bookkeeping, dispatch to the order worker, then journal,
// publish and notify. The order is dispatched before the side work so
// none of it can delay an exit.
func (svc *Service) handleSignal(ctx context.Context, book *signalBook, sig strategy.Signal, now time.Time) {
	if sig.StrategyName == strategy.TelegramSignalName {
		defer svc.saveTGPosition(ctx)
	}
	if !svc.admitSignal(ctx, &sig, now) {
		return
	}
	fnoToken, ok := svc.signalContract(ctx, sig, now)
	if !ok {
		return
	}

	// Index level at the signal, before sig.Price becomes the
	// option LTP (the exit policy's delta reference).
	indexAtSignal := svc.orderExecutor.GetLTP(sig.Token)
	if indexAtSignal <= 0 {
		indexAtSignal = sig.Price
	}

	// ── Get current FNO LTP ──
	var out signalOutcome
	if fnoToken != "" {
		out.currentFNOPrice = svc.orderExecutor.GetLTP(fnoToken)
		if out.currentFNOPrice > 0 {
			sig.Price = out.currentFNOPrice
		}
	}

	posKey := sig.StrategyName + "|" + string(sig.Side)
	sig.MarketState = svc.signalMarketState(book, sig, posKey)

	if sig.Action == strategy.ActionBuy {
		out.stoplossPrice = svc.bookEntry(book, sig, posKey, fnoToken, out.currentFNOPrice, indexAtSignal, now)
	}

	// Order mode is decided BEFORE recording the trade, so an EXIT that
	// causes the profit cap is still LIVE.
	out.orderMode, out.liveMode, out.profitCapHit = svc.orderModeFor(sig)

	out.tradedToken = fnoToken
	if sig.Action == strategy.ActionExit {
		if token, price, ok := svc.bookExit(book, &sig, posKey); ok {
			out.tradedToken = token
			if price > 0 {
				out.currentFNOPrice = price
			}
		}
	}

	// ── Execute FNO order ──
	// Orders go to a per-strategy FIFO worker: a reverse EXIT->BUY pair
	// stays in order without blocking this loop. P&L and portfolio are
	// booked from the executor's fills (see onFill), not here.
	// Skip executor entirely when profit cap is hit — the executor
	// independently decides REAL based on strategy name, so we must
	// gate it here to prevent real orders after the daily cap.
	if out.profitCapHit && sig.Action == strategy.ActionBuy {
		// Only block NEW entries — EXIT/SELL must always go through
		// so open REAL positions can be closed properly.
		log.Printf("[stratengine] 🛑 PROFIT CAP: skipping new BUY — dailyPnL >= ₹650 (mode=%s) %s",
			out.orderMode, sig.StrategyName)
	} else {
		svc.dispatchOrder(ctx, sig)
	}

	svc.reportSignal(ctx, sig, out, now)
}

// admitSignal applies the entry gates and selects an entry's contract. It
// returns false when the signal stops here (advice, refused or cancelled
// entry). Exits always pass.
func (svc *Service) admitSignal(ctx context.Context, sig *strategy.Signal, now time.Time) bool {
	// Entry advice is published only: it never reaches the order path.
	if sig.Action == strategy.ActionWatchBuy {
		svc.publishAdvice(ctx, *sig, now)
		return false
	}

	// Kill switch: block new entries but allow exits
	if svc.killSwitchActive() && sig.Action == strategy.ActionBuy {
		log.Printf("[stratengine] KILL SWITCH: blocked entry signal %s %s:%s",
			sig.StrategyName, sig.Exchange, sig.Token)
		svc.recordRefusedEntry(ctx, *sig, "kill switch active", now)
		return false
	}

	// Never open fresh positions once the live market session is closed.
	// This prevents delayed 15:29/15:30 signals from re-entering after the
	// EOD auto-exit loop has already flattened the book.
	if sig.Action == strategy.ActionBuy && !markethours.IsMarketOpen(now) {
		log.Printf("[stratengine] MARKET CLOSED: blocked entry signal %s %s:%s at %s",
			sig.StrategyName, sig.Exchange, sig.Token, now.In(markethours.IST).Format("15:04:05"))
		svc.recordRefusedEntry(ctx, *sig, "market closed", now)
		return false
	}

	// Nor after the EOD auto-exit cutoff: the book is being (or has
	// been) flattened for the day.
	if sig.Action == strategy.ActionBuy && svc.pastEODCutoff(now) {
		log.Printf("[stratengine] EOD CUTOFF: blocked entry signal %s %s:%s at %s",
			sig.StrategyName, sig.Exchange, sig.Token, now.In(markethours.IST).Format("15:04:05"))
		svc.recordRefusedEntry(ctx, *sig, "past EOD entry cutoff", now)
		return false
	}

	// An entry naming its own strike: resolve that exact
	// contract. No contract or no premium → refuse the entry
	// (and cancel it in the strategy) rather than buy a
	// different strike.
	if sig.Action == strategy.ActionBuy && sig.Strike > 0 && sig.Leg == "" && sig.FNOToken == "" {
		if err := svc.resolveEntryStrike(ctx, sig, now); err != nil {
			svc.cancelStrategyEntry(ctx, *sig, err.Error(), now)
			return false
		}
	} else if sig.Action == strategy.ActionBuy && sig.StrategyName != strategy.TelegramSignalName {
		// A Telegram call names its own contract; it must not move the
		// shared picker's tokens.
		svc.applyOptionAutomation(ctx, sig, now)
	}
	return true
}

// signalContract is the option token for sig: the entry's own contract,
// else the ATM picker's (or static) token for its side. ok is false when
// an entry has nothing to buy; exits always pass.
func (svc *Service) signalContract(ctx context.Context, sig strategy.Signal, now time.Time) (string, bool) {
	var fnoToken string
	sp := svc.strikePicker.Load()
	switch sig.Side {
	case strategy.SideCall:
		if sp != nil && sp.Resolved() {
			fnoToken = sp.GetCallToken().Token
		} else {
			fnoToken = svc.cfg.CallFNOToken
		}
	case strategy.SidePut:
		if sp != nil && sp.Resolved() {
			fnoToken = sp.GetPutToken().Token
		} else {
			fnoToken = svc.cfg.PutFNOToken
		}
	}

	if sig.Action == strategy.ActionBuy && sig.FNOToken != "" {
		fnoToken = sig.FNOToken
	}
	// Another underlying's entry buys only its own resolved
	// contract, never the NIFTY ATM pair above.
	if sig.Action == strategy.ActionBuy && sig.FNOToken == "" && svc.srExtraFor(sig.StrategyName) != nil {
		svc.cancelStrategyEntry(ctx, sig, "no contract resolved for "+sig.StrategyName, now)
		return "", false
	}

	// No strike picked yet and no static token: nothing to buy.
	// Exits still go through so an open position can always close.
	if sig.Action == strategy.ActionBuy && fnoToken == "" {
		log.Printf("[stratengine] NO STRIKE: blocked entry signal %s:%s — strike not resolved yet",
			sig.StrategyName, sig.Side)
		return "", false
	}
	return fnoToken, true
}

// signalMarketState is the regime label for sig: its own, the strategy's
// automation context, the entry's (for an exit), or one read from the
// reason.
func (svc *Service) signalMarketState(book *signalBook, sig strategy.Signal, posKey string) string {
	marketState := normalizeMarketStateValue(sig.MarketState)
	if marketState == "" {
		if actx, ok := svc.strategyAutomationContext(sig); ok {
			marketState = normalizeMarketStateValue(string(actx.MarketState))
		}
	}
	if marketState == "" && sig.Action == strategy.ActionExit {
		if ms, ok := book.marketStates[posKey]; ok {
			marketState = normalizeMarketStateValue(ms)
		}
	}
	if marketState == "" {
		marketState = inferMarketStateFromReason(sig.Reason)
	}
	return marketState
}

// bookEntry records an entry in the book, the strategy, the live orders
// view and the exit policy, and returns the option stoploss to report.
func (svc *Service) bookEntry(book *signalBook, sig strategy.Signal, posKey, fnoToken string, fnoPrice, indexAtSignal int64, now time.Time) int64 {
	book.fnoPrices[posKey] = fnoPrice
	book.fnoTokens[posKey] = fnoToken
	book.marketStates[posKey] = sig.MarketState
	book.instruments[posKey] = trackedInstrument{token: sig.Token, exchange: sig.Exchange}
	if ps := svc.positionTokenSetterFor(sig.StrategyName); ps != nil && sig.FNOToken != "" {
		ps.SetPositionToken(svc.qualifyFNOTokenFor(sig.StrategyName, sig.FNOToken))
	}
	svc.setStrategyFNOEntryPrice(sig.StrategyName, fnoPrice)
	svc.upsertLiveOrder(sig.StrategyName, sig.Side, fnoToken, fnoPrice)
	svc.openExitPolicy(sig, posKey, fnoToken, fnoPrice, indexAtSignal, now)
	var stoplossPrice int64
	if live, ok := svc.liveOrderPayload(sig.StrategyName, sig.Side); ok {
		stoplossPrice = live.StoplossPrice
	}
	// Compute FNO option SL price from strategy-specific FNO config
	if stoplossPrice == 0 {
		if hardSL, _, _, ok := svc.stopConfig(sig.StrategyName); ok && hardSL > 0 && fnoPrice > 0 {
			stoplossPrice = int64(float64(fnoPrice) * (1 - hardSL/100))
		}
	}
	return stoplossPrice
}

// orderModeFor decides PAPER, PAPER_CAP or REAL for sig. The profit cap
// only turns new BUYs to paper; EXIT signals stay REAL so they close real
// positions.
func (svc *Service) orderModeFor(sig strategy.Signal) (orderMode string, liveMode, profitCapHit bool) {
	orderMode = "PAPER"
	if svc.orderExecutor.IsRealStrategy(sig.StrategyName) && svc.cfg.LiveOrders {
		dailyPnL := svc.pnlTracker.GetStrategyDailyPnL(sig.StrategyName)
		// 10 points profit cap for 65 qty = 65 × 10 × 100 paise = 65,000 paise
		const profitCapPaise int64 = 65000 // 65 qty × 10 points = ₹650.00
		if dailyPnL >= profitCapPaise && sig.Action == strategy.ActionBuy {
			return "PAPER_CAP", false, true
		}
		return "REAL", true, false
	}
	return orderMode, false, false
}

// bookExit closes the entry's bookkeeping for an exit signal: it pins sig
// to the entry's instrument and attaches the entry option price. It
// returns the entry's option token and its LTP (0 if none) when the entry
// is known.
func (svc *Service) bookExit(book *signalBook, sig *strategy.Signal, posKey string) (token string, ltp int64, ok bool) {
	// After a restart the book is empty: take the held contract and fill
	// from the executor's record.
	if _, known := book.fnoTokens[posKey]; !known {
		if token, price, restored := svc.restoredEntryFNO(posKey); restored {
			book.fnoTokens[posKey] = token
			if _, has := book.fnoPrices[posKey]; !has && price > 0 {
				book.fnoPrices[posKey] = price
			}
		}
	}
	svc.normalizeExitInstrument(sig, book.instruments, posKey)
	svc.closeExitPolicy(posKey)
	svc.removeLiveOrder(sig.StrategyName, sig.Side)
	if entryToken, has := book.fnoTokens[posKey]; has && entryToken != "" {
		token, ok = entryToken, true
		if exitLTP := svc.orderExecutor.GetLTP(entryToken); exitLTP > 0 {
			ltp = exitLTP
			sig.Price = exitLTP
		}
		delete(book.fnoTokens, posKey)
	}
	delete(book.instruments, posKey)
	delete(book.marketStates, posKey)
	// Attach entry FNO price to exit signal
	if entryP, has := book.fnoPrices[posKey]; has {
		sig.EntryFNOPrice = entryP
		delete(book.fnoPrices, posKey)
	}
	return token, ltp, ok
}

// reportSignal journals the signal, publishes it on pub:signal with the
// P&L and live orders, and notifies. It runs after dispatch.
func (svc *Service) reportSignal(ctx context.Context, sig strategy.Signal, out signalOutcome, now time.Time) {
	// Journal and publish a copy naming the traded option. The
	// order was already dispatched with sig unchanged.
	rec := sig
	rec.FNOToken = svc.unqualifyToken(out.tradedToken)
	if rec.FNOSymbol == "" || sig.Action == strategy.ActionExit {
		if name := svc.fnoSymbolFor(rec.FNOToken); name != "" {
			rec.FNOSymbol = name
		}
	}

	// Journal the signal with live mode and profit cap flags
	if err := svc.journal.Record(rec, now, nil, out.liveMode, out.profitCapHit); err != nil {
		log.Printf("[stratengine] journal write error: %v", err)
	}

	// Publish to Redis PubSub for frontend WS delivery
	sigPayload, err := json.Marshal(map[string]interface{}{
		"strategy_name":     sig.StrategyName,
		"action":            sig.Action,
		"side":              sig.Side,
		"reverse_to":        sig.ReverseTo,
		"market_state":      sig.MarketState,
		"token":             sig.Token,
		"exchange":          sig.Exchange,
		"qty":               sig.Qty,
		"price":             sig.Price,
		"entry_fno_price":   sig.EntryFNOPrice,
		"current_fno_price": out.currentFNOPrice,
		"stoploss_price":    out.stoplossPrice,
		"reason":            sig.Reason,
		"warnings":          sig.Warnings,
		"fno_token":         rec.FNOToken,
		"fno_symbol":        rec.FNOSymbol,
		"pick":              sig.Pick,
		"pick_alt":          sig.PickAlt,
		"order_mode":        out.orderMode,
		"profit_cap_hit":    out.profitCapHit,
		"ts":                now.UTC().Format(time.RFC3339Nano),
	})
	pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
	if err == nil {
		if pubErr := svc.redisWriter.Client().Publish(pctx, model.SignalChannel, string(sigPayload)).Err(); pubErr != nil {
			log.Printf("[stratengine] signal publish error: %v", pubErr)
		}
	}

	// ── Publish P&L summary to Redis ──
	svc.publishPnLSummary(pctx)
	svc.publishLiveOrders(pctx)
	pcancel()

	// Notify
	alert := notification.Alert{
		Level:   notification.AlertInfo,
		Title:   fmt.Sprintf("[%s] %s %s:%s", sig.StrategyName, sig.Action, sig.Exchange, sig.Token),
		Message: sig.Reason,
	}
	if sig.Action == strategy.ActionBuy {
		alert.Level = notification.AlertWarning
		alert.Message = fmt.Sprintf("%s | FNO entry price: ₹%.2f", sig.Reason, float64(out.currentFNOPrice)/100)
	} else if sig.Action == strategy.ActionExit {
		pnl := float64(out.currentFNOPrice-sig.EntryFNOPrice) / 100
		alert.Message = fmt.Sprintf("%s | Entry: ₹%.2f → Exit: ₹%.2f | P&L: ₹%.2f",
			sig.Reason, float64(sig.EntryFNOPrice)/100, float64(out.currentFNOPrice)/100, pnl)
	}
	svc.notifyAsync(alert)

	log.Printf("[stratengine] SIGNAL: %s %s %s:%s fno_ltp=%d entry_fno=%d — %s",
		sig.Action, sig.StrategyName, sig.Exchange, sig.Token, out.currentFNOPrice, sig.EntryFNOPrice, sig.Reason)
}

func expandReverseSignals(sig strategy.Signal) []strategy.Signal {
	if sig.Action != strategy.ActionExit {
		return []strategy.Signal{sig}
	}
	if sig.ReverseTo != strategy.SideCall && sig.ReverseTo != strategy.SidePut {
		return []strategy.Signal{sig}
	}

	closePrice := parseSignalClose(sig.Reason)
	if closePrice <= 0 && sig.Price > 0 {
		closePrice = sig.Price
	}
	buyReason := fmt.Sprintf("ENTRY %s reason=REVERSE_FROM_%s close=%d", sig.ReverseTo, sig.Side, closePrice)

	buySig := sig
	buySig.Action = strategy.ActionBuy
	buySig.Side = sig.ReverseTo
	buySig.ReverseTo = strategy.SideNone
	buySig.EntryFNOPrice = 0
	buySig.Price = 0
	buySig.Reason = buyReason

	return []strategy.Signal{sig, buySig}
}
