# Global option picker — design

Date: 2026-09-29
Status: approved in conversation, pending spec review

## Goal

One option-contract picker used by every strategy (NIFTY50_SR, NIFTY50_RANGE,
NIFTY50_RANGE_IC). A strategy states an **intent** (side, delta band,
expiry rule; for the condor, range edges and a delta cap); the picker chooses
strike and expiry by the same quality rules for all strategies and adds
**zero delay at order time**: selection reads only in-memory state.

It also closes three gaps in today's SR picker:

- **Spread** — no bid/ask spread check today.
- **Premium coverage** — premium known only for strikes on the ladder.
- **Greeks staleness** — greeks up to 60 s old at the pick.

## Decisions (from the conversation)

| Question | Decision |
|---|---|
| Who chooses the strike | Strategy gives intent; picker chooses strike and expiry. RANGE and IC stop fixing their own strikes. |
| Condor intent | Short CE at/above resistance + offset, short PE at/below support − offset, short-leg \|delta\| cap, fixed wing width. |
| Data approach | C: SnapQuote stream for premium and depth; Angel `OptionGreek` refreshed in the background; greeks recomputed locally on spot ticks from Angel's IV. |
| Latency | No network I/O on the order path. |
| Paper fills | At quoted ask (buy) / bid (sell) when the quote is fresh. |
| Spread / quote age defaults | ≤ 2 % of mid, ≤ 3 s (env-configurable). |

## Non-goals

- No change to the real-order gate in `internal/orderexec/executor.go`
  (`NIFTY50_FNO` && live && sessionOK). All three strategies stay paper.
- Exits never go through the picker and are never blocked by it.
- No local IV solving (approach B was rejected).
- No change to candle aggregation.

## Architecture

New package `internal/optionpicker`, consumed by stratengine. It defines the
narrow interfaces it needs (consumer-defined, like `orderexec.PnLTracker`) and
imports neither `stratengine` nor `portfolio`.

```
Angel WS (SnapQuote) ─▶ mdengine ─▶ pub:tick:* (+bid/ask/OI) ─▶ stratengine
                                                                  │
                          ┌───────────────────────────────────────┤
                          ▼                                       ▼
                      QuoteBook  ◀── spot ──▶ LiveGreeks ◀── ChainCache ◀── OptionGreek (15 s, background)
                          │                       │
                          └──────────┬────────────┘
                                     ▼
          signal ─▶ Selector.Pick(intent, now)  (memory only, µs) ─▶ dispatchOrder
                                     │
                                     └─ refusal ─▶ pub:refused
          Universe ─▶ cmd:subscribe_token {mode: 3}  (what to stream)
```

### Units

| Unit | Purpose | Inputs | Output |
|---|---|---|---|
| `ChainCache` | Angel greeks + IV per contract for candidate expiries | `OptionGreek` every 15 s (background goroutine) | snapshot: contracts, spot at fetch, fetch time |
| `QuoteBook` | Latest LTP, best bid, best ask, OI, quote time per option token | SnapQuote ticks | `Quote(token)` |
| `LiveGreeks` | Delta/gamma/theta at current spot | chain snapshot IV + live spot; Black-Scholes | greeks per contract, recomputed ≤ every 250 ms, Universe contracts only |
| `Universe` | Which contracts must stream | spot, candidate expiries, condor edges | subscribe/unsubscribe commands (mode 3) |
| `Selector` | Pure selection | intent, rules, contracts + quotes + greeks | contract(s) or refusal with reason |

The Black-Scholes model in `internal/backtest/optionmodel.go` moves to a shared
package (e.g. `internal/optionmath`) used by both backtest and `LiveGreeks`.

## Intents

```go
type SingleIntent struct {
    Strategy   string
    Option     string  // CE / PE
    DeltaMin   float64 // |delta| band
    DeltaMax   float64
    MinDTE     int     // nearest expiry at least this many days out
    TargetMove int64   // index paise to target, 0 = no cost rule
}

type CondorIntent struct {
    Strategy       string
    ShortCEAtLeast int64   // index points: resistance + offset
    ShortPEAtMost  int64   // index points: support − offset
    MaxShortDelta  float64 // |delta| cap on short legs
    WingWidth      int64   // index points
    MinDTE         int
    MinCreditPct   int64   // net credit ≥ this % of wing width
}
```

| Strategy | Intent |
|---|---|
| NIFTY50_SR | `Single{delta 0.45–0.60, MinDTE 2 (STRAT_SR_MIN_DTE), TargetMove from signal}` |
| NIFTY50_RANGE | `Single{band from deltaBand(otm), MinDTE 1}`: ATM 0.40–0.60 (narrow/medium, breakout), 1 OTM 0.30–0.50 (wide), ATM within `ATMWithinDTE` |
| NIFTY50_RANGE_IC | `Condor{res + ShortOffsetPts, sup − ShortOffsetPts, MaxShortDelta 0.25, WingWidthPts 100, MinDTE 1, MinCreditPct}` |

`strategy.Signal` gains an `Intent` field (one of the two types, or nil).
Strategies stop computing `Strike`/`Legs[].Strike`; the picker fills them.

### Selection

- Single: among contracts of the chosen expiry passing every rule, the one
  whose |delta| is closest to the band midpoint; tie → tighter spread.
- Condor: short CE = lowest strike ≥ `ShortCEAtLeast` whose |delta| ≤ cap and
  which passes the rules; short PE symmetric; longs = shorts ± `WingWidth`.
  Every leg must pass liquidity, spread and freshness; net credit (short bids −
  long asks) must meet `MinCreditPct`. All or nothing.
- Expiry: nearest with `daysBetween(today, expiry) ≥ MinDTE` (never same day).

## Rules

Defaults global; each overridable per strategy by env
(`STRAT_PICK_<RULE>` and `STRAT_<STRATEGY>_PICK_<RULE>`).

| Rule | Applies to | Default | Env |
|---|---|---|---|
| Spread `(ask − bid) / mid` | all legs | ≤ 2 % | `STRAT_PICK_MAX_SPREAD_PCT` |
| Quote age (bid/ask) | all legs | ≤ 3 s | `STRAT_PICK_MAX_QUOTE_AGE` |
| Chain (IV) age | all | ≤ 2 min | `STRAT_PICK_MAX_CHAIN_AGE` |
| Theta per day ÷ premium | bought | ≤ 8 % | existing `STRAT_SR_MAX_THETA_PCT` |
| Gamma near expiry | bought | ≤ 0.005 within 1 day | existing `STRAT_SR_MAX_GAMMA`, `STRAT_SR_GAMMA_DTE` |
| IV | bought ≤ 25 %; sold legs' average ≥ `RangeMinSellIV` | existing | `STRAT_RANGE_MAX_BUY_IV`, `STRAT_RANGE_MIN_SELL_IV` |
| Liquidity (live OI, else volume) | all legs | ≥ 5 000 | existing `STRAT_RANGE_MIN_LIQUIDITY` |
| Cost `|delta| × TargetMove ≥ k × round trip`, round trip = ask − bid (buy at ask, sell at bid) | bought, when TargetMove > 0 | k = 3 | existing `STRAT_RANGE_COST_MULTIPLE` |
| Min DTE | per intent | SR 2, RANGE/IC 1 | per strategy |

Money stays `int64` paise throughout (bid, ask, spread, cost, credit). Greeks
and IV are `float64` (not money).

## Data flow changes

### mdengine

- `cmd:subscribe_token` payload gains `"mode"` (default 2 when absent, so
  current publishers keep working). Options are subscribed with mode 3.
- `rememberDynamicTokens` stores the mode, so re-login re-subscribes in the
  same mode.
- A token moved from Quote to SnapQuote is unsubscribed from Quote to avoid
  duplicate packets.
- `model.Tick` gains `BestBid`, `BestAsk` (paise), `OI`, `QuoteTS`, filled from
  `best_5_buy_data[0]`, `best_5_sell_data[0]`, `open_interest` (already parsed
  by `pkg/smartconnect/websocket.go`). JSON fields are `omitempty`; existing
  consumers ignore them.
- Aggregator unchanged (SnapQuote carries LTP and day volume like Quote).
- Staging (`wssim`): synthetic bid/ask = LTP ± 0.1 %, OI constant, so the
  picker runs end to end.

### stratengine

- `QuoteBook` is fed where `orderExecutor.UpdateLTP` is fed today: one map
  write per tick, no allocation.
- `ChainCache` replaces the `optionChain` 60 s cache and the 30 s loop in
  `sr_strike_view.go`.
- `Universe` replaces `refreshStrikeLadder` and `subscribeSRExpiryLadder`:
  ATM ± 8 strikes on each candidate expiry, plus condor edge strikes and wings
  (± 4 strikes) while RANGE_IC is enabled; re-centre on a 100-point spot move.
  About 70–110 tokens (Angel limit 1000 per connection; only Depth mode has a
  separate quota).
- `resolveEntryStrike` → `picker.Pick(intent, now)`; sets `Strike`,
  `FNOToken`, `FNOSymbol`. `handleBasketSignal` → `picker.PickCondor`.
- Removed after cut-over: `pickSRStrike`, `selectSRContract`,
  `evalSRContracts`, `deltaChecked`, `entryQualityCheck`,
  `basketQualityCheck`, both ladders.

### orderexec (paper path only)

- Paper buy fills at `BestAsk`, paper sell at `BestBid`, when the quote is
  within the quote-age limit; otherwise today's `LTP ± max(bps, min)`
  fallback, so exits always fill.
- The real-order gate is untouched.

### Gateway and dashboard

- `pub:strikesel` / `strikesel:state` / `/api/strikesel` carry the picker
  view for all strategies: rules, live pick per strategy intent, last pick
  per strategy, per-leg bid/ask/spread/quote age.
- FNO instruments tab renders one section per strategy.
- Refusals keep using `pub:refused`, with the failing rule as the reason
  (e.g. `spread 3.4% > 2%`, `quote stale 5.1s`).

## Failure handling

All cases refuse the entry (recorded on `pub:refused`), never substitute a
different contract, never touch exits.

| Situation | Behaviour |
|---|---|
| `OptionGreek` fails | Keep last chain ≤ 2 min, retry every 15 s; then refuse `greeks stale <age>` |
| Candidate not streamed / silent | Candidate fails freshness; Universe subscribes it |
| Quote older than limit | Candidate rejected `quote stale` |
| No spot | Refuse `no spot` |
| Token limit reached | Universe keeps strikes closest to ATM / edges first, logs the trim; others count as `not streamed` |
| mdengine restart / re-login | Dynamic re-subscribe keeps each token's mode |

## Testing

- Selector: table tests per rule (spread, quote age, chain age, delta band,
  theta, gamma, IV, liquidity, cost, MinDTE), condor edge + delta cap + credit,
  tie-breaks.
- LiveGreeks: equals Angel greeks at the snapshot spot within tolerance; moves
  the right direction with spot.
- QuoteBook, Universe: subscribe set, re-centre, trim, no re-subscribe.
- mdengine: SnapQuote packet → `Tick` bid/ask/OI; subscribe command with mode;
  re-login keeps mode.
- Entry path: SR, RANGE, IC through the picker; refusal reaches `pub:refused`.
- Paper fills: at ask/bid; fallback without a fresh quote.
- Benchmark: tick → QuoteBook write, 0 allocs/op.

## Rollout

1. mdengine SnapQuote support and `Tick` fields (no downstream behaviour change).
2. `internal/optionpicker` with tests; Universe streaming; old path decides.
3. `STRAT_PICKER_MODE=shadow`: old logic decides, picker computes in parallel;
   FNO tab shows both for comparison over a few sessions.
4. `STRAT_PICKER_MODE=on`: picker decides for all three strategies; delete the
   old selection code.

## Broker rate limits

Published by Angel One (SmartAPI forum, "Changes in API Rate Limit",
topic 4387):

| Endpoint | Limit |
|---|---|
| searchScrip | 1 / s |
| Market quote (`getMarketData`) | 10 / s, 500 / min, 5000 / h |
| Candle data | 3 / s, 180 / min, 5000 / h |
| Place / modify / cancel order | 20 / s, 500 / min, 1000 / h |
| Login, generate tokens | 1 / s (tokens also 1000 / h) |

`optionGreek` is **not in the published table**. The picker calls it twice
every 15 s (one call per candidate expiry) = 0.13 / s, 8 / min, 480 / h —
below the strictest published per-endpoint limit (1 / s). The ChainCache
backs off to 60 s after an `exceeding access rate` response and logs it;
the chain-age limit (2 min) then decides whether entries are refused.

Consequences for the design:

- **Universe resolves tokens from the offline instrument master only.** A
  burst of ~110 searchScrip calls at 1 / s would take ~2 min and trip the
  limit (what stopped the strike ladder on 2026-09-29). Contracts missing
  from the master are resolved by searchScrip in a throttled background
  queue (≤ 1 / s), never on the order path.
- No market-quote calls are needed (SnapQuote supplies bid/ask); the 10 / s
  quote limit stays free for the gateway's account pages.

## Open points for planning

- Measure `optionGreek` behaviour in production: log every call's latency and
  any rate-limit response for the first sessions after rollout step 2.
- Whether SnapQuote packets arrive when only depth changes (affects quote-age
  semantics); verify with a recorded session.
