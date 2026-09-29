# Global Option Picker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One in-memory option picker that chooses strike and expiry for NIFTY50_SR, NIFTY50_RANGE and NIFTY50_RANGE_IC by the same rules (delta band, spread, quote/chain freshness, theta, gamma, IV, liquidity, cost, min DTE), with zero network I/O on the order path.

**Architecture:** mdengine streams option contracts in SnapQuote mode so every tick carries best bid/ask/OI. A new `internal/optionpicker` package keeps a QuoteBook (live bid/ask), a ChainCache (Angel `OptionGreek` every 15 s in the background) and a Universe (which contracts to stream); `PickSingle` / `PickCondor` compute Black-Scholes greeks at the live spot from the chain's IV and select from memory in microseconds. stratengine maps each strategy's signal to an intent and runs the picker in `off`, `shadow` (old logic decides, picker logged) or `on` mode.

**Tech Stack:** Go 1.x (module `trading-systemv1`, code under `backend/`), Redis pub/sub, Angel One SmartAPI WebSocket v2, React + TypeScript + vitest (frontend).

**Spec:** `docs/superpowers/specs/2026-09-29-global-option-picker-design.md`

## Refinements to the spec (agreed simplifications)

- **Greeks at pick time, not a 250 ms loop.** `LiveGreeks` is `optionmath.GreeksAt` evaluated inside `SelectSingle`/`SelectCondor` for the ~100 streamed contracts (≈ 20 µs). Same result, no background recompute.
- **No strategy package changes.** Intents are built in stratengine (`intentFor`, `condorIntentFor`) from what strategies already emit: SR/RANGE `Strike` + side, IC leg strikes (short strikes already encode range edge + offset).
- **Condor coverage by a wider ladder.** Universe streams ATM ± 12 strikes when RANGE_IC is enabled (± 8 otherwise) instead of tracking the range edges separately.

## Global Constraints

- Money is `int64` paise everywhere (LTP, bid, ask, spread, credit, cost). Greeks and IV are `float64`.
- No network I/O on the order path: `PickSingle` / `PickCondor` read memory only.
- Exits never go through the picker and are never blocked by it.
- The real-order gate in `internal/orderexec/executor.go` (`sig.StrategyName == "NIFTY50_FNO" && oe.live && oe.sessionOK`) is not changed.
- A refused entry never substitutes a different contract; it is refused with a reason (recorded on `pub:refused` via `cancelStrategyEntry` / basket cancel).
- Defaults: spread ≤ 2 % of mid (`STRAT_PICK_MAX_SPREAD_PCT`), quote ≤ 3 s old (`STRAT_PICK_MAX_QUOTE_AGE`), chain ≤ 2 min old (`STRAT_PICK_MAX_CHAIN_AGE`); theta/gamma/IV/liquidity/cost reuse existing `STRAT_SR_*` / `STRAT_RANGE_*` values.
- Mode switch `STRAT_PICKER_MODE` = `off` | `shadow` | `on`, default `shadow`.
- Angel limits: searchScrip 1 / s. Universe resolves tokens from the offline instrument master; misses go to a background queue at ≤ 1 / s, never the order path. `optionGreek` ≤ 2 calls / 15 s, backing off to 60 s after `exceeding access rate`.
- Consumer-defined interfaces: `optionpicker` defines the interfaces it needs and imports neither `stratengine`, `orderexec` nor `portfolio`.
- All commands run from `backend/` (Go) or `frontend/` (npm).

## Review Focus

1. **Market open, no SnapQuote yet** — the first minutes after subscribing have no bid/ask: entries must be refused `quote stale`/`not streamed`, never panic or pick on LTP alone. (Task 6 test `TestSelectSingleRefusesWithoutQuotes`.)
2. **Expiry Tuesday** — the chain holds today's expiry; it must never be picked for any strategy, even with `MinDTE` 0 configured. (Task 6 test `TestSelectSingleNeverSameDay`.)
3. **Spot jumps past the streamed ladder** — candidates outside the Universe have no token/quote: refuse with `not streamed`, and the Universe re-centres on the next refresh. (Task 9 test `TestUniverseRecentresOnSpotMove`, Task 6 reject counting.)
4. **`optionGreek` rate-limited or down** — chain kept ≤ 2 min then entries refused `greeks stale`; cache backs off to 60 s. (Task 8 tests.)
5. **Re-login after options moved to SnapQuote** — the new socket must re-subscribe them in mode 3, not the session's default mode 2; an old command without `mode` still means mode 2. (Task 3 tests.)

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/model/tick.go` (modify) | `Tick` gains `BestBid`, `BestAsk`, `OI`, `QuoteTS` |
| `backend/internal/marketdata/ws/ingest.go` (modify) | parse SnapQuote best bid/ask/OI; per-mode extra subscriptions |
| `backend/internal/marketdata/wssim/ingest.go` (modify) | synthetic bid/ask for staging NFO ticks |
| `backend/internal/mdengine/service.go`, `session.go` (modify) | subscribe command `mode`; remember dynamic tokens per mode |
| `backend/internal/optionmath/bs.go` (create) | Black-Scholes price and greeks (shared by backtest and picker) |
| `backend/internal/backtest/optionmodel.go` (modify) | use `optionmath.Price` |
| `backend/internal/optionpicker/types.go` (create) | `Contract`, `Quote`, `Greeks`, `Candidate`, `Rules`, intents, `Pick`, `CondorPick`, `Rejects` |
| `backend/internal/optionpicker/quotebook.go` (create) | live quotes per token; `BidAsk` for paper fills |
| `backend/internal/optionpicker/selector.go` (create) | pure `SelectSingle`, `SelectCondor` |
| `backend/internal/optionpicker/chaincache.go` (create) | background `OptionGreek` snapshot with backoff |
| `backend/internal/optionpicker/universe.go` (create) | which contracts to stream; token map; throttled search queue |
| `backend/internal/optionpicker/picker.go` (create) | facade: `OnTick`, `Run`, `PickSingle`, `PickCondor`, `Status` |
| `backend/internal/orderexec/strikepicker_legs.go` (modify) | `LookupOn` (instrument master only) |
| `backend/internal/orderexec/executor.go`, `legs.go` (modify) | paper fills at quoted ask/bid via `QuoteSource` |
| `backend/internal/stratengine/picker_wiring.go` (create) | adapters, `intentFor`, shadow/on decision, picker view |
| `backend/internal/stratengine/config.go`, `service.go`, `option_select.go`, `legs.go` (modify) | config, construction, tick feed, entry and basket paths |
| `frontend/src/types/strikesel.ts`, `frontend/src/components/signals/FnoInstrumentsTab.tsx` (modify) | picker section on the FNO tab |

---

### Task 1: SnapQuote fields on `model.Tick`

**Files:**
- Modify: `backend/internal/model/tick.go:10-21`
- Modify: `backend/internal/marketdata/ws/ingest.go:209-243` (`parseTick`)
- Test: `backend/internal/marketdata/ws/ingest_test.go`

**Interfaces:**
- Produces: `model.Tick.BestBid int64`, `BestAsk int64` (paise), `OI int64`, `QuoteTS time.Time` (receive time of the bid/ask; zero when the packet had no depth).

- [ ] **Step 1: Write the failing test** (append to `ingest_test.go`)

```go
func TestParseTickSnapQuoteBestBidAskOI(t *testing.T) {
	recv := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	msg := map[string]interface{}{
		"token": "40712", "exchange_type": 2,
		"last_traded_price":        int64(20300),
		"exchange_timestamp":       recv.UnixMilli(),
		"open_interest":            int64(254000),
		"best_5_buy_data":          []map[string]interface{}{{"price": int64(20280), "quantity": int64(650)}, {"price": int64(20275), "quantity": int64(1300)}},
		"best_5_sell_data":         []map[string]interface{}{{"price": int64(20320), "quantity": int64(325)}},
		"volume_trade_for_the_day": int64(1000),
	}
	tk, err := parseTick(msg, recv)
	if err != nil {
		t.Fatal(err)
	}
	if tk.BestBid != 20280 || tk.BestAsk != 20320 || tk.OI != 254000 || !tk.QuoteTS.Equal(recv) {
		t.Fatalf("tick = %+v", tk)
	}
}

func TestParseTickQuoteModeHasNoDepth(t *testing.T) {
	recv := time.Now().UTC()
	tk, err := parseTick(map[string]interface{}{"token": "99926000", "exchange_type": 1, "last_traded_price": int64(2269000)}, recv)
	if err != nil || tk.BestBid != 0 || tk.BestAsk != 0 || !tk.QuoteTS.IsZero() {
		t.Fatalf("tick = %+v err=%v", tk, err)
	}
}

func TestParseTickSkipsEmptyDepthLevels(t *testing.T) {
	recv := time.Now().UTC()
	tk, _ := parseTick(map[string]interface{}{
		"token": "1", "exchange_type": 2, "last_traded_price": int64(500),
		"best_5_buy_data":  []map[string]interface{}{{"price": int64(0), "quantity": int64(0)}},
		"best_5_sell_data": []map[string]interface{}{},
	}, recv)
	if tk.BestBid != 0 || tk.BestAsk != 0 || !tk.QuoteTS.IsZero() {
		t.Fatalf("empty depth produced a quote: %+v", tk)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/marketdata/ws/ -run 'TestParseTick(SnapQuote|QuoteMode|SkipsEmpty)' -v`
Expected: FAIL to compile — `tk.BestBid undefined`.

- [ ] **Step 3: Add the fields** (`model/tick.go`, after `DayVolume`)

```go
	// SnapQuote only (options): best bid/ask (paise) and open interest.
	// QuoteTS is the local receive time of this bid/ask; zero when the
	// packet carried no depth (Quote/LTP mode, index ticks).
	BestBid int64     `json:"best_bid,omitempty"`
	BestAsk int64     `json:"best_ask,omitempty"`
	OI      int64     `json:"oi,omitempty"`
	QuoteTS time.Time `json:"quote_ts,omitempty"`
```

- [ ] **Step 4: Parse them** (`ws/ingest.go`, in `parseTick` before the `return`, and add the helper below the function)

```go
	bid := bestPrice(msg["best_5_buy_data"])
	ask := bestPrice(msg["best_5_sell_data"])
	var quoteTS time.Time
	if bid > 0 || ask > 0 {
		quoteTS = recvTS
	}
```

and in the returned struct add `BestBid: bid, BestAsk: ask, OI: toInt64(msg["open_interest"]), QuoteTS: quoteTS,`.

```go
// bestPrice returns the first depth level's price (paise), 0 when the level
// is missing or empty. Angel sends best-first.
func bestPrice(v interface{}) int64 {
	levels, ok := v.([]map[string]interface{})
	if !ok || len(levels) == 0 {
		return 0
	}
	if toInt64(levels[0]["quantity"]) <= 0 {
		return 0
	}
	return toInt64(levels[0]["price"])
}
```

- [ ] **Step 5: Run tests**

Run: `cd backend && go test ./internal/marketdata/... ./internal/model/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/model/tick.go backend/internal/marketdata/ws/ingest.go backend/internal/marketdata/ws/ingest_test.go
git commit -m "feat(md): carry SnapQuote best bid/ask and OI on ticks"
```

---

### Task 2: Synthetic quotes in staging

**Files:**
- Modify: `backend/internal/marketdata/wssim/ingest.go:159-176` (`handleMessage`)
- Test: `backend/internal/marketdata/wssim/ingest_test.go` (create if absent; package `wssim`)

**Interfaces:**
- Consumes: Task 1 fields.
- Produces: staging NFO ticks with `BestBid = LTP − s`, `BestAsk = LTP + s`, `s = max(5, LTP/1000)`, `QuoteTS = recvTS`, when the simulator sent no bid/ask.

- [ ] **Step 1: Write the failing test**

```go
package wssim

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestHandleMessageAddsSyntheticQuoteForOptions(t *testing.T) {
	ing := &Ingest{}
	ch := make(chan model.Tick, 2)
	now := time.Now().UTC()
	ing.handleMessage([]byte(`{"token":"40712","exchange":"NFO","price":20000}`), now, ch)
	ing.handleMessage([]byte(`{"token":"99926000","exchange":"NSE","price":2269000}`), now, ch)
	opt, idx := <-ch, <-ch
	if opt.BestBid != 19980 || opt.BestAsk != 20020 || !opt.QuoteTS.Equal(now) {
		t.Fatalf("option quote = %+v", opt)
	}
	if idx.BestBid != 0 || idx.BestAsk != 0 {
		t.Fatalf("index got a quote: %+v", idx)
	}
}
```

- [ ] **Step 2: Run it** — `cd backend && go test ./internal/marketdata/wssim/ -run SyntheticQuote -v` → FAIL (`BestBid 0`).

- [ ] **Step 3: Implement** (in `handleMessage`, right after `tick.TickTS = recvTS`)

```go
	if tick.Exchange == "NFO" && tick.Price > 0 && tick.BestBid == 0 && tick.BestAsk == 0 {
		// Staging has no depth: a ±0.1% spread (min one 5-paise tick) lets
		// the option picker run end to end.
		s := tick.Price / 1000
		if s < 5 {
			s = 5
		}
		tick.BestBid, tick.BestAsk, tick.QuoteTS = tick.Price-s, tick.Price+s, recvTS
	}
```

- [ ] **Step 4: Run** `cd backend && go test ./internal/marketdata/wssim/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/marketdata/wssim/
git commit -m "feat(wssim): synthetic bid/ask for staging option ticks"
```

---

### Task 3: Subscribe options in SnapQuote mode (mdengine)

**Files:**
- Modify: `backend/internal/mdengine/service.go:52-58` (fields), `:238-285` (`listenForDynamicSubscriptions`)
- Modify: `backend/internal/mdengine/session.go:88-103` (`rememberDynamicTokens`, `sessionTokenList`), `:206-213` (ingest config), `:254-272` (dynamic subscribe loop), `:317`
- Modify: `backend/internal/marketdata/ws/ingest.go:44-53` (`IngestConfig`), `:114-116` (`wire`)
- Test: `backend/internal/mdengine/pipeline_test.go:230-250`, `backend/internal/mdengine/dynsub_test.go` (create)

**Interfaces:**
- Produces: command payload `{"exchange_type":2,"tokens":[...],"mode":3}`; absent `mode` = 2.
- Produces: `type dynSub struct{ Mode int; Tokens []smartconnect.TokenListEntry }` on `s.dynamicSubCh`.
- Produces: `IngestConfig.Extra map[int][]smartconnect.TokenListEntry` — extra subscriptions per mode, stored with `AddSubscription` so every (re)connect sends them.
- Produces: `func (s *Service) sessionExtraSubs() map[int][]smartconnect.TokenListEntry`.

- [ ] **Step 1: Write the failing tests** (`dynsub_test.go`)

```go
package mdengine

import (
	"testing"

	"trading-systemv1/pkg/smartconnect"
)

func TestParseSubscribeCommandMode(t *testing.T) {
	sub, ok := parseSubscribeCommand(`{"exchange_type":2,"tokens":["40712"],"mode":3}`)
	if !ok || sub.Mode != 3 || sub.Tokens[0].ExchangeType != 2 || sub.Tokens[0].Tokens[0] != "40712" {
		t.Fatalf("sub = %+v ok=%v", sub, ok)
	}
	old, ok := parseSubscribeCommand(`{"exchange_type":2,"tokens":["40712"]}`)
	if !ok || old.Mode != 2 {
		t.Fatalf("command without mode must default to Quote (2): %+v", old)
	}
	if _, ok := parseSubscribeCommand(`{"exchange_type":2,"tokens":[]}`); ok {
		t.Fatal("empty token list accepted")
	}
}

func TestRememberDynamicTokensKeepsMode(t *testing.T) {
	s := &Service{cfg: Config{TokenList: []smartconnect.TokenListEntry{{ExchangeType: 1, Tokens: []string{"99926000"}}}}}
	s.rememberDynamicTokens(dynSub{Mode: 3, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"40712"}}}})
	s.rememberDynamicTokens(dynSub{Mode: 2, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"57710"}}}})
	extra := s.sessionExtraSubs()
	if len(extra[3]) != 1 || extra[3][0].Tokens[0] != "40712" || len(extra[2]) != 1 || extra[2][0].Tokens[0] != "57710" {
		t.Fatalf("extra = %+v", extra)
	}
	if base := s.sessionTokenList(); len(base) != 1 || base[0].Tokens[0] != "99926000" {
		t.Fatalf("base list must be the configured tokens only: %+v", base)
	}
}
```

Update `pipeline_test.go:238-240` to the new API:

```go
	s.rememberDynamicTokens(dynSub{Mode: 2, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"57710"}}}})
	got := s.sessionExtraSubs()[2]
```

and change that test's assertion to expect `got[0].Tokens[0] == "57710"`.

- [ ] **Step 2: Run** `cd backend && go test ./internal/mdengine/ -run 'SubscribeCommand|KeepsMode' -v` → FAIL to compile.

- [ ] **Step 3: Implement in `service.go`**

Replace the field `dynamicSubCh chan []smartconnect.TokenListEntry` with `dynamicSubCh chan dynSub`, its `make(...)` with `make(chan dynSub, 10)`, and `dynTokens []smartconnect.TokenListEntry` with `dynTokens map[int][]smartconnect.TokenListEntry`. Add:

```go
// dynSub is one dynamic subscription request: tokens in one feed mode.
type dynSub struct {
	Mode   int
	Tokens []smartconnect.TokenListEntry
}

// parseSubscribeCommand reads cmd:subscribe_token. Mode defaults to Quote (2)
// so publishers that predate the field keep their behaviour.
func parseSubscribeCommand(payload string) (dynSub, bool) {
	var cmd struct {
		ExchangeType int      `json:"exchange_type"`
		Tokens       []string `json:"tokens"`
		Mode         int      `json:"mode"`
	}
	if err := json.Unmarshal([]byte(payload), &cmd); err != nil || len(cmd.Tokens) == 0 {
		return dynSub{}, false
	}
	if cmd.Mode == 0 {
		cmd.Mode = smartconnect.ModeQuote
	}
	return dynSub{Mode: cmd.Mode, Tokens: []smartconnect.TokenListEntry{{ExchangeType: cmd.ExchangeType, Tokens: cmd.Tokens}}}, true
}
```

In `listenForDynamicSubscriptions` replace the unmarshal/entry block with:

```go
			sub, ok := parseSubscribeCommand(msg.Payload)
			if !ok {
				log.Printf("[mdengine] ⚠️  invalid subscribe_token command: %s", msg.Payload)
				continue
			}
			log.Printf("[mdengine] 📡 received dynamic subscribe command: mode=%d tokens=%v", sub.Mode, sub.Tokens)
			select {
			case s.dynamicSubCh <- sub:
			default:
				log.Println("[mdengine] ⚠️  dynamicSubCh full, dropping subscribe command")
			}
```

(`smartconnect.ModeQuote = 2` and `ModeSnapQuote = 3` are defined in `pkg/smartconnect/websocket.go:60-63`.)

- [ ] **Step 4: Implement in `session.go`**

```go
// rememberDynamicTokens records dynamically subscribed tokens, per feed mode,
// so a re-login (new socket) subscribes them again in the same mode.
func (s *Service) rememberDynamicTokens(sub dynSub) {
	s.dynMu.Lock()
	if s.dynTokens == nil {
		s.dynTokens = make(map[int][]smartconnect.TokenListEntry)
	}
	s.dynTokens[sub.Mode] = append(s.dynTokens[sub.Mode], sub.Tokens...)
	s.dynMu.Unlock()
}

// sessionTokenList is the configured token list (subscribed in the session mode).
func (s *Service) sessionTokenList() []smartconnect.TokenListEntry {
	return append([]smartconnect.TokenListEntry(nil), s.cfg.TokenList...)
}

// sessionExtraSubs returns the remembered dynamic tokens grouped by mode.
func (s *Service) sessionExtraSubs() map[int][]smartconnect.TokenListEntry {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	out := make(map[int][]smartconnect.TokenListEntry, len(s.dynTokens))
	for m, l := range s.dynTokens {
		out[m] = append([]smartconnect.TokenListEntry(nil), l...)
	}
	return out
}
```

In the `ws.New(ws.IngestConfig{...})` call add `Extra: s.sessionExtraSubs(),`. In the dynamic loop replace the body with:

```go
					case sub, ok := <-s.dynamicSubCh:
						if !ok {
							return
						}
						s.rememberDynamicTokens(sub) // wanted for this session even if the send failed
						if err := ingest.SubscribeTokens(sub.Mode, sub.Tokens); err != nil {
							log.Printf("[mdengine] ⚠️  dynamic subscribe failed: %v", err)
						} else {
							log.Printf("[mdengine] ✅ dynamically subscribed mode=%d tokens: %+v", sub.Mode, sub.Tokens)
						}
```

Line 317 `s.dynTokens = nil` stays (map reset per session).

- [ ] **Step 5: Implement in `ws/ingest.go`**

Add to `IngestConfig`:

```go
	// Extra subscriptions per mode (dynamic tokens remembered for this
	// session, e.g. options in SnapQuote). Stored like TokenList so every
	// (re)connect sends them.
	Extra map[int][]smartconnect.TokenListEntry
```

In `wire`, after `ing.ws.AddSubscription(ing.cfg.SubscribeMode, ing.cfg.TokenList)`:

```go
	for mode, list := range ing.cfg.Extra {
		if len(list) > 0 {
			ing.ws.AddSubscription(mode, list)
		}
	}
```

- [ ] **Step 6: Run** `cd backend && go build ./... && go test ./internal/mdengine/ ./internal/marketdata/...` → PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/mdengine/ backend/internal/marketdata/ws/ingest.go
git commit -m "feat(mdengine): dynamic subscriptions carry a feed mode (SnapQuote for options)"
```

---

### Task 4: `optionmath` — Black-Scholes price and greeks

**Files:**
- Create: `backend/internal/optionmath/bs.go`, `backend/internal/optionmath/bs_test.go`
- Modify: `backend/internal/backtest/optionmodel.go:34-53` (use `optionmath.Price`)

**Interfaces:**
- Produces: `func Price(spot, strike, years, iv, rate float64, call bool) float64`
- Produces: `type Greeks struct{ Delta, Gamma, Theta, Vega float64 }` — Theta per calendar day, Vega per 1 IV point, in the price's unit.
- Produces: `func GreeksAt(spot, strike, years, iv, rate float64, call bool) Greeks` (iv, rate as fractions, e.g. 0.14, 0.065).
- Produces: `func YearsTo(expiry, now time.Time) float64` — to 15:30 IST on the expiry date, floored at 0.

- [ ] **Step 1: Write the failing test**

```go
package optionmath

import (
	"math"
	"testing"
	"time"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// Reference: S=100 K=100 T=1 σ=0.2 r=0.05 (Hull).
func TestBlackScholesReference(t *testing.T) {
	if p := Price(100, 100, 1, 0.2, 0.05, true); !near(p, 10.4506, 1e-3) {
		t.Fatalf("call = %v", p)
	}
	c := GreeksAt(100, 100, 1, 0.2, 0.05, true)
	if !near(c.Delta, 0.6368, 1e-3) || !near(c.Gamma, 0.018762, 1e-5) || !near(c.Vega, 0.37524, 1e-4) || !near(c.Theta, -6.4140/365, 1e-4) {
		t.Fatalf("call greeks = %+v", c)
	}
	p := GreeksAt(100, 100, 1, 0.2, 0.05, false)
	if !near(p.Delta, -0.3632, 1e-3) || !near(p.Gamma, c.Gamma, 1e-9) || !near(p.Theta, -1.6579/365, 1e-4) {
		t.Fatalf("put greeks = %+v", p)
	}
}

func TestGreeksAtExpiryOrZeroIV(t *testing.T) {
	g := GreeksAt(100, 90, 0, 0.2, 0.05, true)
	if g.Delta != 1 || g.Gamma != 0 {
		t.Fatalf("expired ITM call = %+v", g)
	}
	if g := GreeksAt(100, 110, 1, 0, 0.05, true); g.Delta != 0 {
		t.Fatalf("zero-IV OTM call = %+v", g)
	}
}

func TestYearsTo(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60)
	exp := time.Date(2026, 10, 6, 0, 0, 0, 0, ist)
	now := time.Date(2026, 10, 6, 9, 30, 0, 0, ist) // 6h before 15:30
	if y := YearsTo(exp, now); !near(y, 6.0/(365*24), 1e-9) {
		t.Fatalf("years = %v", y)
	}
	if y := YearsTo(exp, exp.Add(20*time.Hour)); y != 0 {
		t.Fatalf("past expiry = %v", y)
	}
}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/optionmath/` → FAIL (package missing).

- [ ] **Step 3: Implement `bs.go`**

```go
// Package optionmath holds Black-Scholes pricing and greeks shared by the
// backtest option model and the live option picker. Prices and greeks are in
// the unit of spot/strike (index points = rupees for NIFTY options).
package optionmath

import (
	"math"
	"time"
)

var ist = time.FixedZone("IST", 5*3600+30*60)

func normCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }
func normPDF(x float64) float64 { return math.Exp(-x*x/2) / math.Sqrt(2*math.Pi) }

// Price is the Black-Scholes price of a European option.
func Price(spot, strike, years, iv, rate float64, call bool) float64 {
	if years <= 0 || iv <= 0 {
		if call {
			return math.Max(spot-strike, 0)
		}
		return math.Max(strike-spot, 0)
	}
	sq := iv * math.Sqrt(years)
	d1 := (math.Log(spot/strike) + (rate+iv*iv/2)*years) / sq
	d2 := d1 - sq
	if call {
		return spot*normCDF(d1) - strike*math.Exp(-rate*years)*normCDF(d2)
	}
	return strike*math.Exp(-rate*years)*normCDF(-d2) - spot*normCDF(-d1)
}

// Greeks: Theta per calendar day, Vega per 1 IV point.
type Greeks struct {
	Delta, Gamma, Theta, Vega float64
}

// GreeksAt returns Black-Scholes greeks. At expiry or with no IV the option
// is its intrinsic value: delta 1/0 (call) or −1/0 (put), others 0.
func GreeksAt(spot, strike, years, iv, rate float64, call bool) Greeks {
	if years <= 0 || iv <= 0 {
		itm := spot > strike
		if !call {
			itm = spot < strike
		}
		var d float64
		if itm {
			d = 1
		}
		if !call {
			d = -d
		}
		return Greeks{Delta: d}
	}
	sqT := math.Sqrt(years)
	sq := iv * sqT
	d1 := (math.Log(spot/strike) + (rate+iv*iv/2)*years) / sq
	d2 := d1 - sq
	pdf := normPDF(d1)
	disc := strike * math.Exp(-rate*years)
	g := Greeks{
		Gamma: pdf / (spot * sq),
		Vega:  spot * pdf * sqT / 100,
	}
	decay := -spot * pdf * iv / (2 * sqT)
	if call {
		g.Delta = normCDF(d1)
		g.Theta = (decay - rate*disc*normCDF(d2)) / 365
	} else {
		g.Delta = normCDF(d1) - 1
		g.Theta = (decay + rate*disc*normCDF(-d2)) / 365
	}
	return g
}

// YearsTo is the time from now to 15:30 IST on expiry's date, in years (≥ 0).
func YearsTo(expiry, now time.Time) float64 {
	e := expiry.In(ist)
	close := time.Date(e.Year(), e.Month(), e.Day(), 15, 30, 0, 0, ist)
	d := close.Sub(now)
	if d <= 0 {
		return 0
	}
	return d.Hours() / (365 * 24)
}
```

- [ ] **Step 4: Use it in backtest** — in `internal/backtest/optionmodel.go` delete `normCDF` and `bsPrice`, import `trading-systemv1/internal/optionmath`, and replace every `bsPrice(` call with `optionmath.Price(` (same arguments). Check with `grep -n "bsPrice\|normCDF" internal/backtest/*.go` → no results.

- [ ] **Step 5: Run** `cd backend && go test ./internal/optionmath/ ./internal/backtest/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/optionmath/ backend/internal/backtest/optionmodel.go
git commit -m "feat(optionmath): shared Black-Scholes price and greeks"
```

---

### Task 5: `optionpicker` types and QuoteBook

**Files:**
- Create: `backend/internal/optionpicker/types.go`, `quotebook.go`, `quotebook_test.go`

**Interfaces:**
- Produces (types.go):

```go
package optionpicker

import "time"

// Contract is one option from the Angel chain, joined with its feed token.
type Contract struct {
	Token, Symbol string
	Strike        int64  // index points
	Option        string // "CE" / "PE"
	Expiry        time.Time
	IV            float64 // percent, e.g. 14.2
	Liquidity     float64 // max(volume, OI) from the chain snapshot
}

// Quote is the latest feed state of one token. Prices in paise.
type Quote struct {
	LTP, Bid, Ask, OI int64
	At                time.Time // receive time of Bid/Ask
}

// Mid returns (bid+ask)/2, or 0 without both sides.
func (q Quote) Mid() int64 {
	if q.Bid <= 0 || q.Ask <= 0 {
		return 0
	}
	return (q.Bid + q.Ask) / 2
}

// Rules are the selection limits. Zero disables a rule unless noted.
type Rules struct {
	MaxSpreadPct float64       // (ask−bid)/mid × 100
	MaxQuoteAge  time.Duration // required (> 0)
	MaxChainAge  time.Duration // required (> 0)
	MaxThetaPct  float64       // bought: |theta|/day ≤ % of premium
	MaxGamma     float64       // bought, within GammaDTE days
	GammaDTE     int
	MaxBuyIV     float64 // percent
	MinSellIV    float64 // percent, average of sold legs
	MinLiquidity float64 // max(live OI, chain liquidity)
	CostMultiple int64   // bought: |delta| × TargetMove ≥ k × (ask − bid)
	RatePct      float64 // risk-free rate for greeks, e.g. 6.5
}

// SingleIntent asks for one bought option.
type SingleIntent struct {
	Strategy           string
	Option             string // "CE" / "PE"
	DeltaMin, DeltaMax float64
	MinDTE             int
	TargetMove         int64 // index paise to target; 0 = no cost rule
}

// CondorIntent asks for a short iron condor anchored at the range edges.
type CondorIntent struct {
	Strategy       string
	ShortCEAtLeast int64 // index points
	ShortPEAtMost  int64
	MaxShortDelta  float64
	WingWidth      int64 // index points
	MinDTE         int
	MinCreditPct   int64 // net credit ≥ this % of the wing width
}

// Pick is a chosen contract with the greeks and quote it was chosen on.
type Pick struct {
	Contract
	Delta, Gamma, Theta, Vega float64
	Quote                     Quote
	DTE                       int
}

// CondorPick is the four legs of a condor and its net credit (paise).
type CondorPick struct {
	ShortCE, LongCE, ShortPE, LongPE Pick
	Credit                           int64
}

// Rejects counts candidates dropped per rule name.
type Rejects map[string]int

// Refusal is returned when nothing passes; Reason names the cause.
type Refusal struct {
	Reason  string
	Rejects Rejects
}

func (r *Refusal) Error() string { return r.Reason }
```

- Produces (quotebook.go): `type QuoteBook struct`, `NewQuoteBook(maxAge time.Duration) *QuoteBook`, `(*QuoteBook).Update(tick model.Tick)`, `(*QuoteBook).Get(token string) (Quote, bool)`, `(*QuoteBook).BidAsk(token string, now time.Time) (bid, ask int64, ok bool)` (ok only when both sides present and `now − At ≤ maxAge`).

- [ ] **Step 1: Write the failing test** (`quotebook_test.go`)

```go
package optionpicker

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestQuoteBookKeepsLastQuoteAndFreshness(t *testing.T) {
	b := NewQuoteBook(3 * time.Second)
	t0 := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	b.Update(model.Tick{Token: "40712", Price: 20300, BestBid: 20280, BestAsk: 20320, OI: 9, QuoteTS: t0})
	b.Update(model.Tick{Token: "40712", Price: 20310}) // LTP-only packet keeps the last bid/ask
	q, ok := b.Get("40712")
	if !ok || q.LTP != 20310 || q.Bid != 20280 || q.Ask != 20320 || q.OI != 9 || !q.At.Equal(t0) {
		t.Fatalf("quote = %+v", q)
	}
	if bid, ask, ok := b.BidAsk("40712", t0.Add(3*time.Second)); !ok || bid != 20280 || ask != 20320 {
		t.Fatal("fresh quote rejected")
	}
	if _, _, ok := b.BidAsk("40712", t0.Add(3*time.Second+time.Millisecond)); ok {
		t.Fatal("stale quote accepted")
	}
	if _, _, ok := b.BidAsk("unknown", t0); ok {
		t.Fatal("unknown token accepted")
	}
}

func BenchmarkQuoteBookUpdate(b *testing.B) {
	qb := NewQuoteBook(3 * time.Second)
	tk := model.Tick{Token: "40712", Price: 20300, BestBid: 20280, BestAsk: 20320, QuoteTS: time.Now()}
	qb.Update(tk)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qb.Update(tk)
	}
}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/optionpicker/ -run QuoteBook -v` → FAIL (package missing).

- [ ] **Step 3: Implement `quotebook.go`**

```go
package optionpicker

import (
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// QuoteBook holds the latest quote per option token. Update is on the tick
// path: one map write, no allocation once a token is known.
type QuoteBook struct {
	maxAge time.Duration
	mu     sync.RWMutex
	q      map[string]Quote
}

func NewQuoteBook(maxAge time.Duration) *QuoteBook {
	return &QuoteBook{maxAge: maxAge, q: make(map[string]Quote, 128)}
}

// Update stores the tick's LTP and, when present, its bid/ask/OI. A packet
// without depth keeps the previous bid/ask (and its time).
func (b *QuoteBook) Update(t model.Tick) {
	b.mu.Lock()
	q := b.q[t.Token]
	if t.Price > 0 {
		q.LTP = t.Price
	}
	if !t.QuoteTS.IsZero() {
		q.Bid, q.Ask, q.At = t.BestBid, t.BestAsk, t.QuoteTS
	}
	if t.OI > 0 {
		q.OI = t.OI
	}
	b.q[t.Token] = q
	b.mu.Unlock()
}

func (b *QuoteBook) Get(token string) (Quote, bool) {
	b.mu.RLock()
	q, ok := b.q[token]
	b.mu.RUnlock()
	return q, ok
}

// BidAsk returns a fresh two-sided quote (for paper fills).
func (b *QuoteBook) BidAsk(token string, now time.Time) (bid, ask int64, ok bool) {
	q, found := b.Get(token)
	if !found || q.Bid <= 0 || q.Ask <= 0 || now.Sub(q.At) > b.maxAge {
		return 0, 0, false
	}
	return q.Bid, q.Ask, true
}
```

- [ ] **Step 4: Run** `cd backend && go test ./internal/optionpicker/ -run QuoteBook -bench QuoteBookUpdate -benchmem` → PASS; benchmark shows `0 allocs/op`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/optionpicker/
git commit -m "feat(optionpicker): types and live QuoteBook"
```

---

### Task 6: Selector — single option

**Files:**
- Create: `backend/internal/optionpicker/selector.go`, `selector_test.go`

**Interfaces:**
- Consumes: Task 4 `optionmath.GreeksAt`, `optionmath.YearsTo`; Task 5 types.
- Produces:

```go
// Env is the market state a selection reads. Spot in paise.
type Env struct {
	Spot    int64
	Now     time.Time
	ChainAt time.Time
	Quote   func(token string) (Quote, bool)
}
func SelectSingle(chain []Contract, in SingleIntent, r Rules, env Env) (Pick, Rejects, error)
func DaysBetween(now, expiry time.Time) int // IST calendar days
```

Reject keys, in check order: `"not streamed"`, `"quote stale"`, `"spread"`, `"no iv"`, `"delta"`, `"theta"`, `"gamma"`, `"iv"`, `"liquidity"`, `"cost"`.

- [ ] **Step 1: Write the failing tests** (`selector_test.go`)

```go
package optionpicker

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var ist = time.FixedZone("IST", 5*3600+30*60)

// Wed 30 Sep 2026 11:30 IST; weekly expiries Tue 06 Oct and Tue 13 Oct.
var (
	now    = time.Date(2026, 9, 30, 11, 30, 0, 0, ist)
	exp1   = time.Date(2026, 10, 6, 0, 0, 0, 0, ist)
	exp2   = time.Date(2026, 10, 13, 0, 0, 0, 0, ist)
	rules  = Rules{MaxSpreadPct: 2, MaxQuoteAge: 3 * time.Second, MaxChainAge: 2 * time.Minute, MaxThetaPct: 8, MaxGamma: 0.005, GammaDTE: 1, MaxBuyIV: 25, MinLiquidity: 5000, CostMultiple: 3, RatePct: 6.5}
	spot   = int64(2270000) // 22700.00
	callIn = SingleIntent{Strategy: "T", Option: "CE", DeltaMin: 0.45, DeltaMax: 0.60, MinDTE: 1}
)

func ctr(strike int64, opt string, exp time.Time) Contract {
	return Contract{Token: opt + itoa(strike) + exp.Format("0102"), Symbol: "S" + itoa(strike) + opt, Strike: strike, Option: opt, Expiry: exp, IV: 14, Liquidity: 100000}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// quotes gives every token a fresh tight quote around the Black-Scholes mid.
func quotes(over map[string]Quote) func(string) (Quote, bool) {
	return func(tok string) (Quote, bool) {
		if q, ok := over[tok]; ok {
			return q, true
		}
		return Quote{LTP: 15000, Bid: 14990, Ask: 15010, OI: 50000, At: now}, true
	}
}

func env(q func(string) (Quote, bool)) Env { return Env{Spot: spot, Now: now, ChainAt: now, Quote: q} }

func ladder(opt string, exp time.Time) []Contract {
	var c []Contract
	for k := int64(22400); k <= 23000; k += 50 {
		c = append(c, ctr(k, opt, exp))
	}
	return c
}

func TestSelectSingleClosestToBandMiddle(t *testing.T) {
	p, _, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(quotes(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Strike != 22700 && p.Strike != 22650 { // |delta| nearest 0.525 is at/just below ATM
		t.Fatalf("strike %d delta %.3f", p.Strike, p.Delta)
	}
	if p.Delta < 0.45 || p.Delta > 0.60 || p.DTE != 6 || p.Quote.Bid != 14990 {
		t.Fatalf("pick = %+v", p)
	}
}

func TestSelectSingleRejectsWideSpreadForNextInBand(t *testing.T) {
	best, _, _ := SelectSingle(ladder("CE", exp1), callIn, rules, env(quotes(nil)))
	wide := map[string]Quote{best.Token: {LTP: 15000, Bid: 14500, Ask: 15500, OI: 50000, At: now}}
	p, rej, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(quotes(wide)))
	if err != nil || p.Strike == best.Strike || rej["spread"] != 1 {
		t.Fatalf("pick %d rej %v err %v", p.Strike, rej, err)
	}
}

func TestSelectSingleRefusesWithoutQuotes(t *testing.T) {
	none := func(string) (Quote, bool) { return Quote{}, false }
	_, rej, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(none))
	var r *Refusal
	if !errors.As(err, &r) || rej["quote stale"] == 0 {
		t.Fatalf("err %v rej %v", err, rej)
	}
	stale := func(string) (Quote, bool) { return Quote{Bid: 14990, Ask: 15010, At: now.Add(-4 * time.Second)}, true }
	if _, rej, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(stale)); err == nil || rej["quote stale"] == 0 {
		t.Fatalf("stale quotes accepted: %v", rej)
	}
}

func TestSelectSingleNotStreamed(t *testing.T) {
	c := ladder("CE", exp1)
	for i := range c {
		c[i].Token = ""
	}
	if _, rej, err := SelectSingle(c, callIn, rules, env(quotes(nil))); err == nil || rej["not streamed"] != len(c) {
		t.Fatalf("rej %v err %v", rej, err)
	}
}

func TestSelectSingleNeverSameDay(t *testing.T) {
	expiryDay := time.Date(2026, 10, 6, 10, 0, 0, 0, ist)
	chain := append(ladder("CE", exp1), ladder("CE", exp2)...)
	in := callIn
	in.MinDTE = 0
	e := env(quotes(nil))
	e.Now, e.ChainAt = expiryDay, expiryDay
	p, _, err := SelectSingle(chain, in, rules, e)
	if err != nil || !sameDate(p.Expiry, exp2) {
		t.Fatalf("expiry %v err %v, want 13 Oct", p.Expiry, err)
	}
}

func TestSelectSingleMinDTE(t *testing.T) {
	monday := time.Date(2026, 10, 5, 10, 0, 0, 0, ist)
	in := callIn
	in.MinDTE = 2
	e := env(quotes(nil))
	e.Now, e.ChainAt = monday, monday
	p, _, err := SelectSingle(append(ladder("CE", exp1), ladder("CE", exp2)...), in, rules, e)
	if err != nil || !sameDate(p.Expiry, exp2) || p.DTE != 8 {
		t.Fatalf("pick %+v err %v", p, err)
	}
}

func TestSelectSingleStaleChainAndNoSpot(t *testing.T) {
	e := env(quotes(nil))
	e.ChainAt = now.Add(-3 * time.Minute)
	if _, _, err := SelectSingle(ladder("CE", exp1), callIn, rules, e); err == nil || !strings.Contains(err.Error(), "greeks stale") {
		t.Fatalf("err %v", err)
	}
	e = env(quotes(nil))
	e.Spot = 0
	if _, _, err := SelectSingle(ladder("CE", exp1), callIn, rules, e); err == nil || err.Error() != "no spot" {
		t.Fatalf("err %v", err)
	}
}

func TestSelectSingleIVAndLiquidityAndCost(t *testing.T) {
	c := ladder("CE", exp1)
	for i := range c {
		c[i].IV = 30
	}
	if _, rej, err := SelectSingle(c, callIn, rules, env(quotes(nil))); err == nil || rej["iv"] == 0 {
		t.Fatalf("high IV accepted: %v", rej)
	}
	thin := func(string) (Quote, bool) { return Quote{Bid: 14990, Ask: 15010, OI: 10, At: now}, true }
	c = ladder("CE", exp1)
	for i := range c {
		c[i].Liquidity = 10
	}
	if _, rej, err := SelectSingle(c, callIn, rules, env(thin)); err == nil || rej["liquidity"] == 0 {
		t.Fatalf("thin contract accepted: %v", rej)
	}
	in := callIn
	in.TargetMove = 1000 // 10 points × 0.5 delta = 500 paise < 3 × 20 spread? no: 500 ≥ 60 passes
	if _, _, err := SelectSingle(ladder("CE", exp1), in, rules, env(quotes(nil))); err != nil {
		t.Fatalf("cost rule too strict: %v", err)
	}
	in.TargetMove = 100 // 1 point × 0.5 = 50 paise < 3 × 20 = 60 → every candidate fails cost
	if _, rej, err := SelectSingle(ladder("CE", exp1), in, rules, env(quotes(nil))); err == nil || rej["cost"] == 0 {
		t.Fatalf("cost rule not applied: %v", rej)
	}
}

func sameDate(a, b time.Time) bool {
	a, b = a.In(ist), b.In(ist)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/optionpicker/ -run SelectSingle -v` → FAIL (undefined `SelectSingle`).

- [ ] **Step 3: Implement `selector.go`**

```go
package optionpicker

import (
	"fmt"
	"math"
	"time"

	"trading-systemv1/internal/optionmath"
)

var istZone = time.FixedZone("IST", 5*3600+30*60)

type Env struct {
	Spot    int64 // paise
	Now     time.Time
	ChainAt time.Time
	Quote   func(token string) (Quote, bool)
}

// DaysBetween counts IST calendar days from now's date to expiry's date.
func DaysBetween(now, expiry time.Time) int {
	a, b := now.In(istZone), expiry.In(istZone)
	d0 := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, istZone)
	d1 := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, istZone)
	return int(d1.Sub(d0).Hours() / 24)
}

// checkEnv refuses selection on missing spot or a stale chain.
func checkEnv(r Rules, env Env) error {
	if env.Spot <= 0 {
		return &Refusal{Reason: "no spot"}
	}
	if age := env.Now.Sub(env.ChainAt); env.ChainAt.IsZero() || age > r.MaxChainAge {
		return &Refusal{Reason: fmt.Sprintf("greeks stale %s", age.Truncate(time.Second))}
	}
	return nil
}

// pickExpiry is the nearest expiry of opt at least minDTE (≥ 1) days out.
func pickExpiry(chain []Contract, opt string, now time.Time, minDTE int) (time.Time, int, bool) {
	if minDTE < 1 {
		minDTE = 1 // never same-day expiry
	}
	var best time.Time
	for _, c := range chain {
		if c.Option != opt || c.Expiry.IsZero() || DaysBetween(now, c.Expiry) < minDTE {
			continue
		}
		if best.IsZero() || c.Expiry.Before(best) {
			best = c.Expiry
		}
	}
	return best, DaysBetween(now, best), !best.IsZero()
}

// evaluated is a candidate with quote and live greeks, or the rule it failed.
type evaluated struct {
	pick   Pick
	failed string
}

// baseCheck applies the rules every leg needs: streamed, fresh two-sided
// quote, spread, IV present. Greeks are computed at the live spot.
func baseCheck(c Contract, dte int, r Rules, env Env) evaluated {
	if c.Token == "" {
		return evaluated{failed: "not streamed"}
	}
	q, ok := env.Quote(c.Token)
	if !ok || q.Bid <= 0 || q.Ask <= 0 || q.Ask < q.Bid || env.Now.Sub(q.At) > r.MaxQuoteAge {
		return evaluated{failed: "quote stale"}
	}
	mid := q.Mid()
	if r.MaxSpreadPct > 0 && float64(q.Ask-q.Bid)*100 > float64(mid)*r.MaxSpreadPct {
		return evaluated{failed: "spread"}
	}
	if c.IV <= 0 {
		return evaluated{failed: "no iv"}
	}
	g := optionmath.GreeksAt(float64(env.Spot)/100, float64(c.Strike), optionmath.YearsTo(c.Expiry, env.Now),
		c.IV/100, r.RatePct/100, c.Option == "CE")
	return evaluated{pick: Pick{Contract: c, Delta: g.Delta, Gamma: g.Gamma, Theta: g.Theta, Vega: g.Vega, Quote: q, DTE: dte}}
}

func liquidityOK(p Pick, r Rules) bool {
	return r.MinLiquidity <= 0 || math.Max(float64(p.Quote.OI), p.Liquidity) >= r.MinLiquidity
}

// buyCheck adds the bought-option rules to a base-checked pick.
func buyCheck(p Pick, in SingleIntent, r Rules) string {
	d := math.Abs(p.Delta)
	premium := float64(p.Quote.Mid()) / 100 // rupees
	switch {
	case d < in.DeltaMin || d > in.DeltaMax:
		return "delta"
	case r.MaxThetaPct > 0 && math.Abs(p.Theta)*100 > premium*r.MaxThetaPct:
		return "theta"
	case r.MaxGamma > 0 && p.DTE <= r.GammaDTE && p.Gamma > r.MaxGamma:
		return "gamma"
	case r.MaxBuyIV > 0 && p.IV > r.MaxBuyIV:
		return "iv"
	case !liquidityOK(p, r):
		return "liquidity"
	case r.CostMultiple > 0 && in.TargetMove > 0 &&
		in.TargetMove*int64(math.Round(d*1000))/1000 < r.CostMultiple*(p.Quote.Ask-p.Quote.Bid):
		return "cost"
	}
	return ""
}

// SelectSingle picks the bought option closest to the delta band's middle
// (tie → tighter spread) on the nearest expiry ≥ MinDTE days out.
func SelectSingle(chain []Contract, in SingleIntent, r Rules, env Env) (Pick, Rejects, error) {
	rej := Rejects{}
	if err := checkEnv(r, env); err != nil {
		return Pick{}, rej, err
	}
	expiry, dte, ok := pickExpiry(chain, in.Option, env.Now, in.MinDTE)
	if !ok {
		return Pick{}, rej, &Refusal{Reason: fmt.Sprintf("no %s expiry %d+ days out", in.Option, max(in.MinDTE, 1))}
	}
	mid := (in.DeltaMin + in.DeltaMax) / 2
	var best Pick
	found := false
	for _, c := range chain {
		if c.Option != in.Option || DaysBetween(env.Now, c.Expiry) != dte || !c.Expiry.Equal(expiry) {
			continue
		}
		ev := baseCheck(c, dte, r, env)
		if ev.failed == "" {
			ev.failed = buyCheck(ev.pick, in, r)
		}
		if ev.failed != "" {
			rej[ev.failed]++
			continue
		}
		p := ev.pick
		if !found {
			best, found = p, true
			continue
		}
		db, dp := math.Abs(math.Abs(best.Delta)-mid), math.Abs(math.Abs(p.Delta)-mid)
		if dp < db || (dp == db && p.Quote.Ask-p.Quote.Bid < best.Quote.Ask-best.Quote.Bid) {
			best = p
		}
	}
	if !found {
		return Pick{}, rej, &Refusal{Reason: fmt.Sprintf("no %s passes (dte %d; rejected %s)", in.Option, dte, rej), Rejects: rej}
	}
	return best, rej, nil
}
```

`Rejects` prints via `%s` as `map[...]`; add a `String()` for readable reasons:

```go
// String lists rejects as "delta 12, spread 1" in rule order.
func (r Rejects) String() string {
	order := []string{"not streamed", "quote stale", "spread", "no iv", "delta", "theta", "gamma", "iv", "liquidity", "cost", "credit"}
	s := ""
	for _, k := range order {
		if n := r[k]; n > 0 {
			if s != "" {
				s += ", "
			}
			s += fmt.Sprintf("%s %d", k, n)
		}
	}
	if s == "" {
		return "none"
	}
	return s
}
```

(Put `String` in `types.go` next to `Rejects`, importing `fmt` there.)

- [ ] **Step 4: Run** `cd backend && go test ./internal/optionpicker/ -run SelectSingle -v` → PASS. If `TestSelectSingleClosestToBandMiddle` picks another strike, print `p.Delta` for 22650/22700/22750 and adjust only the test's accepted strikes to the one(s) whose |delta| is nearest 0.525 — the rule, not the strike, is what is under test.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/optionpicker/
git commit -m "feat(optionpicker): single-option selector with spread, freshness and greek rules"
```

---

### Task 7: Selector — iron condor

**Files:**
- Modify: `backend/internal/optionpicker/selector.go`
- Test: `backend/internal/optionpicker/condor_test.go`

**Interfaces:**
- Produces: `func SelectCondor(chain []Contract, in CondorIntent, r Rules, env Env) (CondorPick, Rejects, error)`.
- Rules: sold legs need base check + `|delta| ≤ MaxShortDelta` + liquidity; bought wings need base check + liquidity. Short CE = lowest strike ≥ `ShortCEAtLeast` whose short and wing (`+WingWidth`) both pass; short PE = highest strike ≤ `ShortPEAtMost` likewise (`−WingWidth`). Credit = shortCE.Bid + shortPE.Bid − longCE.Ask − longPE.Ask (paise) ≥ `WingWidth × 100 × MinCreditPct / 100`. Sold legs' average IV ≥ `MinSellIV`. Reject keys add `"credit"`, `"sell iv"`.

- [ ] **Step 1: Write the failing test**

```go
package optionpicker

import (
	"errors"
	"testing"
)

var condorIn = CondorIntent{Strategy: "IC", ShortCEAtLeast: 22900, ShortPEAtMost: 22500, MaxShortDelta: 0.25, WingWidth: 100, MinDTE: 1, MinCreditPct: 10}

func condorChain() []Contract {
	c := append(ladder("CE", exp1), ladder("PE", exp1)...)
	return append(c, ctr(23050, "CE", exp1), ctr(23100, "CE", exp1), ctr(22350, "PE", exp1), ctr(22300, "PE", exp1))
}

// richQuotes: every leg bid 3990 / ask 4010 (credit = −40 paise).
func richQuotes(tok string) (Quote, bool) {
	return Quote{Bid: 3990, Ask: 4010, OI: 50000, At: now}, true
}

func TestSelectCondorAnchorsAtEdgesWithDeltaCap(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 0 // flat test quotes give a negative credit; the credit rule has its own test
	cp, _, err := SelectCondor(condorChain(), in, rules, env(richQuotes))
	if err != nil {
		t.Fatal(err)
	}
	if cp.ShortCE.Strike < 22900 || cp.LongCE.Strike != cp.ShortCE.Strike+100 ||
		cp.ShortPE.Strike > 22500 || cp.LongPE.Strike != cp.ShortPE.Strike-100 {
		t.Fatalf("legs = %d/%d %d/%d", cp.ShortCE.Strike, cp.LongCE.Strike, cp.ShortPE.Strike, cp.LongPE.Strike)
	}
	if cp.ShortCE.Delta > 0.25 || cp.ShortPE.Delta < -0.25 {
		t.Fatalf("short deltas %.3f %.3f", cp.ShortCE.Delta, cp.ShortPE.Delta)
	}
	if cp.Credit != (3990+3990)-(4010+4010) {
		t.Fatalf("credit = %d", cp.Credit)
	}
}

func TestSelectCondorRefusesThinCredit(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 50 // needs 5000 paise; richQuotes gives negative credit
	_, rej, err := SelectCondor(condorChain(), in, rules, env(richQuotes))
	var r *Refusal
	if !errors.As(err, &r) || rej["credit"] != 1 {
		t.Fatalf("err %v rej %v", err, rej)
	}
}

func TestSelectCondorMovesOutWhenWingNotStreamed(t *testing.T) {
	chain := condorChain()
	for i := range chain {
		if chain[i].Option == "CE" && chain[i].Strike == 23000 {
			chain[i].Token = "" // wing of the 22900 short
		}
	}
	in := condorIn
	in.MinCreditPct = 0
	cp, _, err := SelectCondor(chain, in, rules, env(richQuotes))
	if err != nil || cp.ShortCE.Strike != 22950 {
		t.Fatalf("short CE %d err %v, want 22950", cp.ShortCE.Strike, err)
	}
}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/optionpicker/ -run SelectCondor -v` → FAIL.

- [ ] **Step 3: Implement** (append to `selector.go`)

```go
// SelectCondor builds a short iron condor past the range edges. All legs
// or nothing.
func SelectCondor(chain []Contract, in CondorIntent, r Rules, env Env) (CondorPick, Rejects, error) {
	rej := Rejects{}
	if err := checkEnv(r, env); err != nil {
		return CondorPick{}, rej, err
	}
	expiry, dte, ok := pickExpiry(chain, "CE", env.Now, in.MinDTE)
	if !ok {
		return CondorPick{}, rej, &Refusal{Reason: "no condor expiry"}
	}
	byKey := make(map[string]Contract, len(chain))
	for _, c := range chain {
		if c.Expiry.Equal(expiry) {
			byKey[fmt.Sprintf("%d%s", c.Strike, c.Option)] = c
		}
	}
	leg := func(strike int64, opt string, short bool) (Pick, bool) {
		c, ok := byKey[fmt.Sprintf("%d%s", strike, opt)]
		if !ok {
			rej["not streamed"]++
			return Pick{}, false
		}
		ev := baseCheck(c, dte, r, env)
		if ev.failed == "" && !liquidityOK(ev.pick, r) {
			ev.failed = "liquidity"
		}
		if ev.failed == "" && short && math.Abs(ev.pick.Delta) > in.MaxShortDelta {
			ev.failed = "delta"
		}
		if ev.failed != "" {
			rej[ev.failed]++
			return Pick{}, false
		}
		return ev.pick, true
	}
	side := func(opt string, from, dir int64) (Pick, Pick, bool) {
		const maxSteps = 12
		step := int64(50)
		for i := int64(0); i < maxSteps; i++ {
			k := from + dir*i*step
			s, ok := leg(k, opt, true)
			if !ok {
				continue
			}
			l, ok := leg(k+dir*in.WingWidth, opt, false)
			if !ok {
				continue
			}
			return s, l, true
		}
		return Pick{}, Pick{}, false
	}
	sCE, lCE, okCE := side("CE", in.ShortCEAtLeast, +1)
	sPE, lPE, okPE := side("PE", in.ShortPEAtMost, -1)
	if !okCE || !okPE {
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("no condor legs pass (rejected %s)", rej), Rejects: rej}
	}
	if r.MinSellIV > 0 && (sCE.IV+sPE.IV)/2 < r.MinSellIV {
		rej["sell iv"]++
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("short legs IV %.1f%% < %.1f%%", (sCE.IV+sPE.IV)/2, r.MinSellIV), Rejects: rej}
	}
	credit := sCE.Quote.Bid + sPE.Quote.Bid - lCE.Quote.Ask - lPE.Quote.Ask
	if need := in.WingWidth * 100 * in.MinCreditPct / 100; credit < need {
		rej["credit"]++
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("credit %d < %d paise", credit, need), Rejects: rej}
	}
	return CondorPick{ShortCE: sCE, LongCE: lCE, ShortPE: sPE, LongPE: lPE, Credit: credit}, rej, nil
}
```

- [ ] **Step 4: Run** `cd backend && go test ./internal/optionpicker/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/optionpicker/
git commit -m "feat(optionpicker): iron condor selector anchored at range edges"
```

---

### Task 8: ChainCache

**Files:**
- Create: `backend/internal/optionpicker/chaincache.go`, `chaincache_test.go`

**Interfaces:**
- Produces:

```go
type ChainSource interface {
	Load(now time.Time) ([]Contract, error) // contracts without Token/Symbol
}
func NewChainCache(src ChainSource, every, backoff time.Duration) *ChainCache
func (c *ChainCache) Refresh(now time.Time) time.Duration // next wait
func (c *ChainCache) Snapshot() ([]Contract, time.Time)   // copy-free read; at = zero before first success
func (c *ChainCache) LastError() error
```

- [ ] **Step 1: Write the failing test**

```go
package optionpicker

import (
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	out  []Contract
	err  error
	hits int
}

func (f *fakeSource) Load(time.Time) ([]Contract, error) { f.hits++; return f.out, f.err }

func TestChainCacheKeepsLastGoodAndBacksOff(t *testing.T) {
	src := &fakeSource{out: []Contract{{Strike: 22700, Option: "CE"}}}
	c := NewChainCache(src, 15*time.Second, 60*time.Second)
	t0 := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	if wait := c.Refresh(t0); wait != 15*time.Second {
		t.Fatalf("wait %v", wait)
	}
	src.err = errors.New("Angel One API rate limit: Access denied because of exceeding access rate")
	if wait := c.Refresh(t0.Add(15 * time.Second)); wait != 60*time.Second {
		t.Fatalf("rate limit wait %v, want 60s backoff", wait)
	}
	got, at := c.Snapshot()
	if len(got) != 1 || !at.Equal(t0) || c.LastError() == nil {
		t.Fatalf("snapshot %v at %v err %v", got, at, c.LastError())
	}
	src.err = errors.New("timeout")
	if wait := c.Refresh(t0.Add(75 * time.Second)); wait != 15*time.Second {
		t.Fatalf("plain error wait %v", wait)
	}
}
```

- [ ] **Step 2: Run** → FAIL (undefined).

- [ ] **Step 3: Implement `chaincache.go`**

```go
package optionpicker

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

type ChainSource interface {
	Load(now time.Time) ([]Contract, error)
}

// ChainCache refreshes the option chain in the background. A failed load
// keeps the last good snapshot; selection refuses once it is older than
// Rules.MaxChainAge.
type ChainCache struct {
	src            ChainSource
	every, backoff time.Duration

	mu        sync.RWMutex
	contracts []Contract
	at        time.Time
	lastErr   error
}

func NewChainCache(src ChainSource, every, backoff time.Duration) *ChainCache {
	return &ChainCache{src: src, every: every, backoff: backoff}
}

func (c *ChainCache) Refresh(now time.Time) time.Duration {
	start := time.Now()
	out, err := c.src.Load(now)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = err
		if strings.Contains(err.Error(), "exceeding access rate") {
			log.Printf("[optionpicker] option chain rate-limited, backing off %s: %v", c.backoff, err)
			return c.backoff
		}
		log.Printf("[optionpicker] option chain load failed: %v", err)
		return c.every
	}
	c.contracts, c.at, c.lastErr = out, now, nil
	log.Printf("[optionpicker] option chain: %d contracts in %s", len(out), time.Since(start).Truncate(time.Millisecond))
	return c.every
}

// Snapshot returns the last good chain. Callers must not modify it.
func (c *ChainCache) Snapshot() ([]Contract, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contracts, c.at
}

func (c *ChainCache) LastError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

// Run refreshes until ctx ends; active reports whether to call the broker
// now (market hours).
func (c *ChainCache) Run(ctx context.Context, active func(time.Time) bool) {
	wait := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		now := time.Now()
		if !active(now) {
			wait = c.every
			continue
		}
		wait = c.Refresh(now)
	}
}
```

The load-latency log line serves the spec's open point "measure optionGreek behaviour in production".

- [ ] **Step 4: Run** `cd backend && go test ./internal/optionpicker/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/optionpicker/
git commit -m "feat(optionpicker): background option chain cache with rate-limit backoff"
```

---

### Task 9: Universe

**Files:**
- Create: `backend/internal/optionpicker/universe.go`, `universe_test.go`
- Modify: `backend/internal/orderexec/strikepicker_legs.go` (add `LookupOn`), `strikepicker_legs_test.go`

**Interfaces:**
- Produces (orderexec): `func (sp *StrikePicker) LookupOn(expiry time.Time, strike int64, optionType string) (StrikeInfo, bool)` — instrument master (or the `lookup` test hook) only, never SearchScrip; caches like `ResolveStrikeOn`.
- Produces (optionpicker):

```go
type Resolver interface {
	Lookup(expiry time.Time, strike int64, opt string) (token, symbol string, ok bool) // fast, offline
	Search(expiry time.Time, strike int64, opt string) (token, symbol string, err error) // broker, ≤ 1/s
}
type Subscriber interface{ SubscribeOptions(tokens []string) } // SnapQuote
func NewUniverse(res Resolver, sub Subscriber, strikes int, step int64) *Universe
func (u *Universe) Refresh(spot int64, expiries []time.Time)            // re-centre when needed
func (u *Universe) Token(expiry time.Time, strike int64, opt string) (token, symbol string, ok bool)
func (u *Universe) RunSearch(ctx context.Context, every time.Duration)  // drains misses at ≤ 1 per `every`
func (u *Universe) Size() int
```

Re-centre rule: rebuild when the ATM strike (spot rounded to `step`) moved by ≥ 2 strikes (100 points at step 50) or the expiry set changed.

- [ ] **Step 1: Write the failing orderexec test** (append to `strikepicker_legs_test.go`)

```go
func TestLookupOnNeverSearches(t *testing.T) {
	sp := NewStrikePicker(nil)
	sp.lookup = func(symbol string) (string, int64, error) {
		if symbol == "NIFTY13OCT2622700CE" {
			return "7002", 65, nil
		}
		return "", 0, errors.New("not in master")
	}
	exp := time.Date(2026, 10, 13, 0, 0, 0, 0, istZone)
	if info, ok := sp.LookupOn(exp, 22700, "CE"); !ok || info.Token != "7002" {
		t.Fatalf("info=%+v ok=%v", info, ok)
	}
	if _, ok := sp.LookupOn(exp, 22750, "CE"); ok {
		t.Fatal("miss reported as found")
	}
}
```

The `lookup` hook stands in for the instrument master in tests; in production `LookupOn` must call `GetInstrumentMaster().GetInstrument(symbol)` only.

- [ ] **Step 2: Implement `LookupOn`** (append to `strikepicker_legs.go`)

```go
// LookupOn is ResolveStrikeOn without the SearchScrip fallback: the offline
// instrument master (or the test hook) only. Used where a broker call is not
// allowed (the option picker's universe; SearchScrip is limited to 1/s).
func (sp *StrikePicker) LookupOn(expiry time.Time, strike int64, optionType string) (StrikeInfo, bool) {
	optionType = strings.ToUpper(optionType)
	symbol := fmt.Sprintf("NIFTY%s%d%s", strings.ToUpper(expiry.In(istZone).Format("02Jan06")), strike, optionType)
	sp.mu.RLock()
	cached, ok := sp.legCache[symbol]
	sp.mu.RUnlock()
	if ok {
		return cached, true
	}
	var token string
	var lot int64
	if sp.lookup != nil {
		t, l, err := sp.lookup(symbol)
		if err != nil {
			return StrikeInfo{}, false
		}
		token, lot = t, l
	} else {
		inst, err := GetInstrumentMaster().GetInstrument(symbol)
		if err != nil || inst.Token == "" {
			return StrikeInfo{}, false
		}
		token = inst.Token
		lot, _ = strconv.ParseInt(strings.TrimSpace(inst.LotSize), 10, 64)
	}
	info := StrikeInfo{Token: token, Symbol: symbol, Strike: strike, LotSize: lot}
	sp.mu.Lock()
	if sp.legCache == nil {
		sp.legCache = make(map[string]StrikeInfo)
	}
	sp.legCache[symbol] = info
	sp.mu.Unlock()
	return info, true
}
```

Run `cd backend && go test ./internal/orderexec/ -run LookupOn -v` → PASS.

- [ ] **Step 3: Write the failing Universe test** (`universe_test.go`)

```go
package optionpicker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeRes struct{ missing map[string]bool; searched []string }

func (f *fakeRes) key(e time.Time, k int64, o string) string { return fmt.Sprintf("%s%d%s", e.Format("0102"), k, o) }
func (f *fakeRes) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	if f.missing[f.key(e, k, o)] {
		return "", "", false
	}
	return "T" + f.key(e, k, o), "S" + f.key(e, k, o), true
}
func (f *fakeRes) Search(e time.Time, k int64, o string) (string, string, error) {
	f.searched = append(f.searched, f.key(e, k, o))
	if k == 99999 {
		return "", "", errors.New("no")
	}
	return "T" + f.key(e, k, o), "S" + f.key(e, k, o), nil
}

type fakeSub struct{ got []string }

func (f *fakeSub) SubscribeOptions(t []string) { f.got = append(f.got, t...) }

func TestUniverseSubscribesLadderOnce(t *testing.T) {
	res, sub := &fakeRes{}, &fakeSub{}
	u := NewUniverse(res, sub, 2, 50)
	u.Refresh(2270000, []time.Time{exp1})
	if len(sub.got) != 10 { // (2×2+1) strikes × CE/PE
		t.Fatalf("subscribed %d: %v", len(sub.got), sub.got)
	}
	u.Refresh(2272000, []time.Time{exp1}) // same ATM (22700): no rebuild
	if len(sub.got) != 10 {
		t.Fatalf("re-subscribed: %d", len(sub.got))
	}
	if tok, _, ok := u.Token(exp1, 22650, "PE"); !ok || tok != "T100622650PE" {
		t.Fatalf("token %q ok %v", tok, ok)
	}
}

func TestUniverseRecentresOnSpotMove(t *testing.T) {
	res, sub := &fakeRes{}, &fakeSub{}
	u := NewUniverse(res, sub, 2, 50)
	u.Refresh(2270000, []time.Time{exp1})
	u.Refresh(2280000, []time.Time{exp1}) // ATM 22800: +2 strikes → re-centre
	if _, _, ok := u.Token(exp1, 22900, "CE"); !ok {
		t.Fatal("new edge strike not in universe")
	}
	if len(sub.got) != 10+4 { // only 22850 and 22900 CE/PE are new
		t.Fatalf("subscribed %d", len(sub.got))
	}
}

func TestUniverseSearchesMissesInBackground(t *testing.T) {
	res := &fakeRes{missing: map[string]bool{"100622700CE": true}}
	sub := &fakeSub{}
	u := NewUniverse(res, sub, 0, 50)
	u.Refresh(2270000, []time.Time{exp1})
	if _, _, ok := u.Token(exp1, 22700, "CE"); ok {
		t.Fatal("miss resolved without search")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	u.RunSearch(ctx, 10*time.Millisecond)
	if tok, _, ok := u.Token(exp1, 22700, "CE"); !ok || tok != "T100622700CE" || len(res.searched) != 1 {
		t.Fatalf("tok %q ok %v searched %v", tok, ok, res.searched)
	}
}
```

- [ ] **Step 4: Run** → FAIL (undefined).

- [ ] **Step 5: Implement `universe.go`**

```go
package optionpicker

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

type Resolver interface {
	Lookup(expiry time.Time, strike int64, opt string) (token, symbol string, ok bool)
	Search(expiry time.Time, strike int64, opt string) (token, symbol string, err error)
}

type Subscriber interface {
	SubscribeOptions(tokens []string)
}

type ukey struct {
	day    string // expiry date YYYY-MM-DD (IST)
	strike int64
	opt    string
}

type uval struct{ token, symbol string }

// Universe decides which option contracts stream in SnapQuote: ATM ± strikes
// on each candidate expiry. Tokens come from the offline resolver; misses are
// searched in the background at ≤ 1 per `every` (searchScrip allows 1/s).
type Universe struct {
	res     Resolver
	sub     Subscriber
	strikes int
	step    int64

	mu         sync.RWMutex
	tokens     map[ukey]uval
	subscribed map[string]bool
	atm        int64
	expKey     string
	pending    []ukey
	pendingSet map[ukey]bool
	expiries   map[string]time.Time
}

func NewUniverse(res Resolver, sub Subscriber, strikes int, step int64) *Universe {
	return &Universe{res: res, sub: sub, strikes: strikes, step: step,
		tokens: map[ukey]uval{}, subscribed: map[string]bool{}, pendingSet: map[ukey]bool{}, expiries: map[string]time.Time{}}
}

func dayKey(t time.Time) string { return t.In(istZone).Format("2006-01-02") }

// Refresh rebuilds the wanted set when ATM moved ≥ 2 strikes or the expiry
// set changed, and subscribes contracts not yet subscribed.
func (u *Universe) Refresh(spot int64, expiries []time.Time) {
	if spot <= 0 || len(expiries) == 0 {
		return
	}
	atm := (spot/100 + u.step/2) / u.step * u.step
	days := make([]string, 0, len(expiries))
	for _, e := range expiries {
		days = append(days, dayKey(e))
	}
	sort.Strings(days)
	expKey := ""
	for _, d := range days {
		expKey += d + ","
	}

	u.mu.Lock()
	moved := u.atm == 0 || abs64(atm-u.atm) >= 2*u.step
	if !moved && expKey == u.expKey {
		u.mu.Unlock()
		return
	}
	u.atm, u.expKey = atm, expKey
	var fresh []string
	for _, e := range expiries {
		u.expiries[dayKey(e)] = e
		for i := -u.strikes; i <= u.strikes; i++ {
			for _, opt := range [...]string{"CE", "PE"} {
				k := ukey{dayKey(e), atm + int64(i)*u.step, opt}
				v, ok := u.tokens[k]
				if !ok {
					tok, sym, found := u.res.Lookup(e, k.strike, opt)
					if !found {
						if !u.pendingSet[k] {
							u.pendingSet[k] = true
							u.pending = append(u.pending, k)
						}
						continue
					}
					v = uval{tok, sym}
					u.tokens[k] = v
				}
				if !u.subscribed[v.token] {
					u.subscribed[v.token] = true
					fresh = append(fresh, v.token)
				}
			}
		}
	}
	u.mu.Unlock()
	if len(fresh) > 0 {
		u.sub.SubscribeOptions(fresh)
		log.Printf("[optionpicker] universe around %d: +%d contracts (%d streamed)", atm, len(fresh), u.Size())
	}
}

func (u *Universe) Token(expiry time.Time, strike int64, opt string) (string, string, bool) {
	u.mu.RLock()
	v, ok := u.tokens[ukey{dayKey(expiry), strike, opt}]
	u.mu.RUnlock()
	return v.token, v.symbol, ok
}

func (u *Universe) Size() int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.subscribed)
}

// RunSearch resolves offline misses by broker search, one per `every`.
func (u *Universe) RunSearch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u.mu.Lock()
		if len(u.pending) == 0 {
			u.mu.Unlock()
			continue
		}
		k := u.pending[0]
		u.pending = u.pending[1:]
		delete(u.pendingSet, k)
		exp := u.expiries[k.day]
		u.mu.Unlock()

		tok, sym, err := u.res.Search(exp, k.strike, k.opt)
		if err != nil {
			log.Printf("[optionpicker] search %s %d%s: %v", k.day, k.strike, k.opt, err)
			continue
		}
		u.mu.Lock()
		u.tokens[k] = uval{tok, sym}
		isNew := !u.subscribed[tok]
		u.subscribed[tok] = true
		u.mu.Unlock()
		if isNew {
			u.sub.SubscribeOptions([]string{tok})
		}
	}
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
```

- [ ] **Step 6: Run** `cd backend && go test ./internal/optionpicker/ ./internal/orderexec/` → PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/optionpicker/ backend/internal/orderexec/strikepicker_legs.go backend/internal/orderexec/strikepicker_legs_test.go
git commit -m "feat(optionpicker): universe of streamed contracts, offline tokens, throttled search"
```

---

### Task 10: Picker facade

**Files:**
- Create: `backend/internal/optionpicker/picker.go`, `picker_test.go`

**Interfaces:**
- Consumes: Tasks 5–9.
- Produces:

```go
const SpotToken = "99926000" // NIFTY 50 index

type Config struct {
	Rules         Rules
	LadderStrikes int           // e.g. 8, or 12 with the condor
	StrikeStep    int64         // 50
	ChainEvery    time.Duration // 15s
	ChainBackoff  time.Duration // 60s
	SearchEvery   time.Duration // 1s (searchScrip 1/s)
}
func New(cfg Config, src ChainSource, res Resolver, sub Subscriber) *Picker
func (p *Picker) OnTick(t model.Tick)
func (p *Picker) Quotes() *QuoteBook
func (p *Picker) Run(ctx context.Context, active func(time.Time) bool)
func (p *Picker) PickSingle(in SingleIntent, now time.Time) (Pick, Rejects, error)
func (p *Picker) PickCondor(in CondorIntent, now time.Time) (CondorPick, Rejects, error)
func (p *Picker) Status(now time.Time) Status
type Status struct {
	ChainAt  time.Time
	ChainErr string
	Streamed int
	Spot     int64
}
```

`PickSingle`/`PickCondor` join the chain snapshot with Universe tokens (a contract without a Universe token keeps `Token == ""` → `not streamed`) and read quotes from the QuoteBook. `Run` starts the chain loop, the search loop, and a 1 s loop calling `Universe.Refresh(spot, chainExpiries)`.

- [ ] **Step 1: Write the failing test**

```go
package optionpicker

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestPickerJoinsChainUniverseAndQuotes(t *testing.T) {
	src := &fakeSource{out: ladder("CE", exp1)}
	for i := range src.out {
		src.out[i].Token, src.out[i].Symbol = "", "" // the chain has no tokens
	}
	res, sub := &fakeRes{}, &fakeSub{}
	p := New(Config{Rules: rules, LadderStrikes: 4, StrikeStep: 50, ChainEvery: 15 * time.Second, ChainBackoff: time.Minute, SearchEvery: time.Second}, src, res, sub)
	p.OnTick(model.Tick{Token: SpotToken, Price: spot})
	p.chain.Refresh(now)
	p.refreshUniverse()
	for _, tok := range sub.got {
		p.OnTick(model.Tick{Token: tok, Price: 15000, BestBid: 14990, BestAsk: 15010, OI: 50000, QuoteTS: now})
	}
	pick, _, err := p.PickSingle(callIn, now)
	if err != nil {
		t.Fatal(err)
	}
	if pick.Token == "" || pick.Symbol == "" || pick.Quote.Ask != 15010 {
		t.Fatalf("pick = %+v", pick)
	}
	if st := p.Status(now); st.Streamed != len(sub.got) || st.Spot != spot || !st.ChainAt.Equal(now) {
		t.Fatalf("status = %+v", st)
	}
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement `picker.go`**

```go
package optionpicker

import (
	"context"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/model"
)

const SpotToken = "99926000"

type Config struct {
	Rules         Rules
	LadderStrikes int
	StrikeStep    int64
	ChainEvery    time.Duration
	ChainBackoff  time.Duration
	SearchEvery   time.Duration
}

type Status struct {
	ChainAt  time.Time
	ChainErr string
	Streamed int
	Spot     int64
}

// Picker chooses option contracts from memory: chain snapshot (background),
// universe tokens and live quotes. Pick* never perform I/O.
type Picker struct {
	cfg    Config
	chain  *ChainCache
	quotes *QuoteBook
	univ   *Universe
	spot   atomic.Int64
}

func New(cfg Config, src ChainSource, res Resolver, sub Subscriber) *Picker {
	return &Picker{
		cfg:    cfg,
		chain:  NewChainCache(src, cfg.ChainEvery, cfg.ChainBackoff),
		quotes: NewQuoteBook(cfg.Rules.MaxQuoteAge),
		univ:   NewUniverse(res, sub, cfg.LadderStrikes, cfg.StrikeStep),
	}
}

// OnTick is on the tick path: an atomic store or one QuoteBook write.
func (p *Picker) OnTick(t model.Tick) {
	if t.Token == SpotToken {
		if t.Price > 0 {
			p.spot.Store(t.Price)
		}
		return
	}
	p.quotes.Update(t)
}

func (p *Picker) Quotes() *QuoteBook { return p.quotes }

func (p *Picker) Run(ctx context.Context, active func(time.Time) bool) {
	go p.chain.Run(ctx, active)
	go p.univ.RunSearch(ctx, p.cfg.SearchEvery)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshUniverse()
		}
	}
}

// refreshUniverse re-centres the universe on spot over the chain's expiries.
func (p *Picker) refreshUniverse() {
	contracts, _ := p.chain.Snapshot()
	seen := map[string]time.Time{}
	for _, c := range contracts {
		if !c.Expiry.IsZero() {
			seen[dayKey(c.Expiry)] = c.Expiry
		}
	}
	exps := make([]time.Time, 0, len(seen))
	for _, e := range seen {
		exps = append(exps, e)
	}
	p.univ.Refresh(p.spot.Load(), exps)
}

// candidates joins the chain snapshot with universe tokens.
func (p *Picker) candidates() ([]Contract, time.Time) {
	contracts, at := p.chain.Snapshot()
	out := make([]Contract, len(contracts))
	for i, c := range contracts {
		c.Token, c.Symbol, _ = p.univ.Token(c.Expiry, c.Strike, c.Option)
		out[i] = c
	}
	return out, at
}

func (p *Picker) env(now, chainAt time.Time) Env {
	return Env{Spot: p.spot.Load(), Now: now, ChainAt: chainAt, Quote: p.quotes.Get}
}

func (p *Picker) PickSingle(in SingleIntent, now time.Time) (Pick, Rejects, error) {
	c, at := p.candidates()
	return SelectSingle(c, in, p.cfg.Rules, p.env(now, at))
}

func (p *Picker) PickCondor(in CondorIntent, now time.Time) (CondorPick, Rejects, error) {
	c, at := p.candidates()
	return SelectCondor(c, in, p.cfg.Rules, p.env(now, at))
}

func (p *Picker) Status(now time.Time) Status {
	_, at := p.chain.Snapshot()
	st := Status{ChainAt: at, Streamed: p.univ.Size(), Spot: p.spot.Load()}
	if err := p.chain.LastError(); err != nil {
		st.ChainErr = err.Error()
	}
	return st
}
```

`candidates` allocates one slice per pick (~100 contracts) — acceptable at signal rate; do not call it per tick.

- [ ] **Step 4: Run** `cd backend && go test -race ./internal/optionpicker/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/optionpicker/
git commit -m "feat(optionpicker): picker facade joining chain, universe and quotes"
```

---

### Task 11: Paper fills at the quoted ask/bid

**Files:**
- Modify: `backend/internal/orderexec/executor.go:630-712` (`executePaper`, add `paperFill`), config/struct fields near `NewOrderExecutor`
- Modify: `backend/internal/orderexec/legs.go:47,72`
- Test: `backend/internal/orderexec/slippage_test.go`

**Interfaces:**
- Produces:

```go
// QuoteSource gives a fresh two-sided quote for paper fills (optionpicker.QuoteBook).
type QuoteSource interface {
	BidAsk(token string, now time.Time) (bid, ask int64, ok bool)
}
func (oe *OrderExecutor) SetQuoteSource(q QuoteSource)
func (oe *OrderExecutor) paperFill(direction, token string, ltp int64) int64
```

- [ ] **Step 1: Write the failing test** (append to `slippage_test.go`, reusing the executor setup already in that file for `paperFillPrice`)

```go
type fixedQuotes struct {
	bid, ask int64
	ok       bool
}

func (f fixedQuotes) BidAsk(string, time.Time) (int64, int64, bool) { return f.bid, f.ask, f.ok }

func TestPaperFillUsesQuotedAskBid(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]", PaperSlippageBps: 50, PaperSlippageMinPaise: 5})
	oe.SetQuoteSource(fixedQuotes{bid: 20280, ask: 20320, ok: true})
	if got := oe.paperFill("BUY", "40712", 20300); got != 20320 {
		t.Fatalf("buy fill %d, want ask 20320", got)
	}
	if got := oe.paperFill("SELL", "40712", 20300); got != 20280 {
		t.Fatalf("sell fill %d, want bid 20280", got)
	}
	oe.SetQuoteSource(fixedQuotes{ok: false}) // stale/no quote → LTP ± slippage
	if got := oe.paperFill("BUY", "40712", 10000); got != 10050 {
		t.Fatalf("fallback buy fill %d, want 10050", got)
	}
}
```

(If `NewOrderExecutor` in this file's existing tests takes other required fields, copy them from the existing `paperFillPrice` test at `slippage_test.go:12`.)

- [ ] **Step 2: Run** `cd backend && go test ./internal/orderexec/ -run PaperFillUsesQuoted -v` → FAIL.

- [ ] **Step 3: Implement** — add to `executor.go` near `paperFillPrice`:

```go
// QuoteSource gives a fresh two-sided quote for paper fills.
type QuoteSource interface {
	BidAsk(token string, now time.Time) (bid, ask int64, ok bool)
}

// SetQuoteSource makes paper fills cross the real spread when a fresh quote
// exists (buy at ask, sell at bid). Real orders are unaffected.
func (oe *OrderExecutor) SetQuoteSource(q QuoteSource) {
	oe.mu.Lock()
	oe.quotes = q
	oe.mu.Unlock()
}

// paperFill is the paper fill price: quoted ask/bid when fresh, else
// paperFillPrice (LTP ± slippage), so an exit always fills.
func (oe *OrderExecutor) paperFill(direction, token string, ltp int64) int64 {
	oe.mu.RLock()
	q := oe.quotes
	oe.mu.RUnlock()
	if q != nil {
		if bid, ask, ok := q.BidAsk(token, time.Now()); ok {
			if direction == "BUY" {
				return ask
			}
			return bid
		}
	}
	return oe.paperFillPrice(direction, ltp)
}
```

Add field `quotes QuoteSource` to the `OrderExecutor` struct. Replace:
- `executor.go:646` `fill := oe.paperFillPrice(direction, ltp)` → `fill := oe.paperFill(direction, fnoToken, ltp)`
- `legs.go:47` `ltp := oe.paperFillPrice(closeDir, oe.GetLTP(rec.Token))` → `ltp := oe.paperFill(closeDir, rec.Token, oe.GetLTP(rec.Token))`
- `legs.go:72` `ltp := oe.paperFillPrice(openDir, oe.ltp[sig.FNOToken])` → `ltp := oe.paperFill(openDir, sig.FNOToken, oe.ltp[sig.FNOToken])` — **check the lock**: line 72 reads `oe.ltp` directly, so it runs under `oe.mu`. `paperFill` takes `oe.mu.RLock()`; if line 72 holds `oe.mu` (write lock) this deadlocks. In that case read `q := oe.quotes` there under the held lock and inline the same logic, or restructure so the LTP read and fill happen outside the lock. Verify with `go test -race ./internal/orderexec/` and the existing leg tests.

`PaperSlippage(ltp)` (used by the old cost rule) stays unchanged.

- [ ] **Step 4: Run** `cd backend && go test -race ./internal/orderexec/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/orderexec/
git commit -m "feat(orderexec): paper fills cross the quoted spread when fresh"
```

---

### Task 12: stratengine wiring — config, adapters, intents, shadow/on

**Files:**
- Create: `backend/internal/stratengine/picker_wiring.go`, `picker_wiring_test.go`
- Modify: `backend/internal/stratengine/config.go` (fields + env), `service.go` (fields, construction, tick feed, Run), `option_select.go` (`resolveEntryStrike`, `publishFNOSubscription` mode), `legs.go` (`handleBasketSignal`, `expandLegSignals`)

**Interfaces:**
- Consumes: Task 10 `optionpicker.Picker`, Task 11 `SetQuoteSource`, Task 9 `StrikePicker.LookupOn`, existing `StrikePicker.ResolveStrikeOn`, `greeksFor()`, `deltaBand(otm)`, `nearestStrike`, `normIV`.
- Produces (config.go): `PickerMode string` (`STRAT_PICKER_MODE`, default `"shadow"`), `PickMaxSpreadPct float64` (`STRAT_PICK_MAX_SPREAD_PCT`, 2), `PickMaxQuoteAge time.Duration` (`STRAT_PICK_MAX_QUOTE_AGE`, 3s), `PickMaxChainAge time.Duration` (`STRAT_PICK_MAX_CHAIN_AGE`, 2m).
- Produces (picker_wiring.go):

```go
func (svc *Service) newPicker() *optionpicker.Picker                        // nil when mode "off"
func (svc *Service) intentFor(sig strategy.Signal) (optionpicker.SingleIntent, bool)
func (svc *Service) condorIntentFor(sig strategy.Signal) (optionpicker.CondorIntent, bool)
func (svc *Service) pickEntry(sig *strategy.Signal, now time.Time) (decided bool, err error)
func (svc *Service) pickBasket(sig *strategy.Signal, now time.Time) (decided bool, err error)
// shadow/on decisions are stored for the dashboard:
type pickerDecision struct { Strategy, Mode, Result, Reason string; Strike int64; Symbol, Token string; Delta, IV float64; Bid, Ask int64; TS string }
func (svc *Service) recordPickerDecision(d pickerDecision)
```

Semantics of `pickEntry`: mode `off` or no intent → `(false, nil)` (old path runs). Mode `shadow` → run the picker, record the decision, return `(false, nil)`. Mode `on` → run the picker, record; on error `(true, err)` (caller refuses); on success set `sig.Strike`, `sig.FNOToken`, `sig.FNOSymbol` and return `(true, nil)`.

- [ ] **Step 1: Write the failing tests** (`picker_wiring_test.go`)

```go
package stratengine

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func TestIntentForSRAndRange(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax, svc.cfg.SRMinDTE = 0.45, 0.60, 2
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	sr := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 22700, TargetMove: 2000}
	in, ok := svc.intentFor(sr)
	if !ok || in.Option != "PE" || in.DeltaMin != 0.45 || in.DeltaMax != 0.60 || in.MinDTE != 2 || in.TargetMove != 2000 {
		t.Fatalf("SR intent = %+v ok=%v", in, ok)
	}
	svc.nifty50RangeStrategy = strategy.NewNifty50Range(1)
	rg := strategy.Signal{StrategyName: svc.nifty50RangeStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 22750}
	in, ok = svc.intentFor(rg) // 1 strike OTM at spot 22700
	if !ok || in.Option != "CE" || in.DeltaMin != 0.30 || in.DeltaMax != 0.50 || in.MinDTE != 1 {
		t.Fatalf("RANGE intent = %+v ok=%v", in, ok)
	}
	if _, ok := svc.intentFor(strategy.Signal{StrategyName: "OTHER", Action: strategy.ActionBuy}); ok {
		t.Fatal("unknown strategy got an intent")
	}
}

func TestCondorIntentFromLegs(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	sig := strategy.Signal{StrategyName: "NIFTY50_RANGE_IC", Action: strategy.ActionBuy, Legs: []strategy.LegSpec{
		{Leg: strategy.LegLongCE, Strike: 23000, OptionType: "CE"}, {Leg: strategy.LegShortCE, Strike: 22900, OptionType: "CE", Short: true},
		{Leg: strategy.LegLongPE, Strike: 22400, OptionType: "PE"}, {Leg: strategy.LegShortPE, Strike: 22500, OptionType: "PE", Short: true},
	}}
	in, ok := svc.condorIntentFor(sig)
	if !ok || in.ShortCEAtLeast != 22900 || in.ShortPEAtMost != 22500 || in.WingWidth != 100 || in.MaxShortDelta != 0.25 {
		t.Fatalf("condor intent = %+v ok=%v", in, ok)
	}
}

func TestPickEntryShadowKeepsOldPathOnDecides(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax = 0.45, 0.60
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	svc.picker = testPicker(t, svc) // see helper below
	sig := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 22700}
	now := pickerNow

	svc.cfg.PickerMode = "shadow"
	s1 := sig
	if decided, err := svc.pickEntry(&s1, now); decided || err != nil || s1.FNOToken != "" {
		t.Fatalf("shadow decided=%v err=%v token=%q", decided, err, s1.FNOToken)
	}
	if d := svc.lastPickerDecision(svc.srStrategy.Name()); d.Mode != "shadow" || d.Result != "picked" {
		t.Fatalf("shadow decision = %+v", d)
	}

	svc.cfg.PickerMode = "on"
	s2 := sig
	if decided, err := svc.pickEntry(&s2, now); !decided || err != nil || s2.FNOToken == "" || s2.FNOSymbol == "" {
		t.Fatalf("on decided=%v err=%v sig=%+v", decided, err, s2)
	}
}
```

Test helper (same file): a picker built on fakes — chain from `fakeGreeks`-style contracts, an offline resolver that always finds `"T<strike><opt>"`, and quotes injected through `OnTick`:

```go
var pickerNow = time.Date(2026, 9, 30, 6, 0, 0, 0, time.UTC) // Wed 11:30 IST

type staticChain []optionpicker.Contract

func (s staticChain) Load(time.Time) ([]optionpicker.Contract, error) { return s, nil }

type allResolver struct{}

func (allResolver) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	return "T" + itoa(k) + o, "NIFTY" + e.Format("02Jan06") + itoa(k) + o, true
}
func (allResolver) Search(time.Time, int64, string) (string, string, error) { return "", "", nil }

type nopSub struct{ got []string }

func (n *nopSub) SubscribeOptions(t []string) { n.got = append(n.got, t...) }

func testPicker(t *testing.T, svc *Service) *optionpicker.Picker {
	t.Helper()
	var chain staticChain
	for k := int64(22400); k <= 23000; k += 50 {
		for _, o := range []string{"CE", "PE"} {
			chain = append(chain, optionpicker.Contract{Strike: k, Option: o, Expiry: testExpiry, IV: 14, Liquidity: 100000})
		}
	}
	sub := &nopSub{}
	p := optionpicker.New(svc.pickerConfig(), chain, allResolver{}, sub)
	p.OnTick(model.Tick{Token: optionpicker.SpotToken, Price: 2270000})
	optionpicker.RefreshForTest(p, pickerNow) // exported test hook: chain.Refresh + refreshUniverse
	for _, tok := range sub.got {
		p.OnTick(model.Tick{Token: tok, Price: 15000, BestBid: 14990, BestAsk: 15010, OI: 50000, QuoteTS: pickerNow})
	}
	return p
}
```

Add to `optionpicker/picker.go` (exported so other packages' tests can prime a picker):

```go
// RefreshForTest loads the chain and re-centres the universe synchronously.
func RefreshForTest(p *Picker, now time.Time) {
	p.chain.Refresh(now)
	p.refreshUniverse()
}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/stratengine/ -run 'IntentFor|CondorIntent|PickEntry' -v` → FAIL.

- [ ] **Step 3: Implement config** (`config.go`): add fields listed under Interfaces to `Config`; in `LoadConfig`:

```go
		PickerMode:       config.GetEnv("STRAT_PICKER_MODE", "shadow"),
		PickMaxSpreadPct: getEnvFloat("STRAT_PICK_MAX_SPREAD_PCT", 2),
		PickMaxQuoteAge:  getEnvDuration("STRAT_PICK_MAX_QUOTE_AGE", 3*time.Second),
		PickMaxChainAge:  getEnvDuration("STRAT_PICK_MAX_CHAIN_AGE", 2*time.Minute),
```

If `getEnvDuration` does not exist in `config.go`, add it next to `getEnvFloat`:

```go
func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
```

- [ ] **Step 4: Implement `picker_wiring.go`**

```go
package stratengine

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"
)

func (svc *Service) pickerConfig() optionpicker.Config {
	strikes := 8
	if svc.cfg.RangeICEnabled {
		strikes = 12 // condor shorts sit past the range edges, wings 100 pts further
	}
	return optionpicker.Config{
		Rules: optionpicker.Rules{
			MaxSpreadPct: svc.cfg.PickMaxSpreadPct, MaxQuoteAge: svc.cfg.PickMaxQuoteAge, MaxChainAge: svc.cfg.PickMaxChainAge,
			MaxThetaPct: svc.cfg.SRMaxThetaPct, MaxGamma: svc.cfg.SRMaxGamma, GammaDTE: svc.cfg.SRGammaDTE,
			MaxBuyIV: svc.cfg.RangeMaxBuyIV, MinSellIV: svc.cfg.RangeMinSellIV,
			MinLiquidity: float64(svc.cfg.RangeMinLiquidity), CostMultiple: svc.cfg.RangeCostMultiple, RatePct: 6.5,
		},
		LadderStrikes: strikes, StrikeStep: ladderStrikeStep,
		ChainEvery: 15 * time.Second, ChainBackoff: 60 * time.Second, SearchEvery: time.Second,
	}
}

// chainAdapter loads Angel's option chain for the picker.
type chainAdapter struct{ svc *Service }

func (a chainAdapter) Load(now time.Time) ([]optionpicker.Contract, error) {
	src, err := a.svc.greeksFor()
	if err != nil {
		return nil, err
	}
	chain, err := src.LoadOptionChain(now)
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

// resolverAdapter: offline instrument master for Lookup, broker for Search.
type resolverAdapter struct{ sp *orderexec.StrikePicker }

func (r resolverAdapter) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	info, ok := r.sp.LookupOn(e, k, o)
	return info.Token, info.Symbol, ok
}

func (r resolverAdapter) Search(e time.Time, k int64, o string) (string, string, error) {
	info, err := r.sp.ResolveStrikeOn(e, k, o)
	return info.Token, info.Symbol, err
}

// subscriberAdapter subscribes option tokens in SnapQuote.
type subscriberAdapter struct{ svc *Service }

func (s subscriberAdapter) SubscribeOptions(tokens []string) {
	if s.svc.redisWriter != nil {
		s.svc.publishFNOSubscriptionMode(context.Background(), smartSnapQuote, tokens...)
	}
}

const smartSnapQuote = smartconnect.ModeSnapQuote

func (svc *Service) newPicker() *optionpicker.Picker {
	if svc.cfg.PickerMode == "off" || svc.orderExecutor == nil {
		return nil
	}
	sp := orderexec.NewStrikePicker(svc.orderExecutor.GetSmartConnect())
	return optionpicker.New(svc.pickerConfig(), chainAdapter{svc}, resolverAdapter{sp}, subscriberAdapter{svc})
}

func (svc *Service) intentFor(sig strategy.Signal) (optionpicker.SingleIntent, bool) {
	if sig.Action != strategy.ActionBuy || sig.Leg != "" || len(sig.Legs) > 0 {
		return optionpicker.SingleIntent{}, false
	}
	opt := optionTypeFor(sig.Side)
	switch {
	case svc.isSRSignal(&sig):
		return optionpicker.SingleIntent{Strategy: sig.StrategyName, Option: opt,
			DeltaMin: svc.cfg.SRDeltaMin, DeltaMax: svc.cfg.SRDeltaMax, MinDTE: svc.cfg.SRMinDTE, TargetMove: sig.TargetMove}, true
	case svc.nifty50RangeStrategy != nil && sig.StrategyName == svc.nifty50RangeStrategy.Name():
		spot := svc.orderExecutor.GetLTP(optionpicker.SpotToken)
		otm := 0
		if spot > 0 && sig.Strike > 0 {
			atm := nearestStrike(spot, ladderStrikeStep)
			d := sig.Strike - atm
			if sig.Side == strategy.SidePut {
				d = -d
			}
			if d > 0 {
				otm = int(d / ladderStrikeStep)
			}
		}
		lo, hi := deltaBand(otm)
		return optionpicker.SingleIntent{Strategy: sig.StrategyName, Option: opt, DeltaMin: lo, DeltaMax: hi, MinDTE: 1, TargetMove: sig.TargetMove}, true
	}
	return optionpicker.SingleIntent{}, false
}

func (svc *Service) condorIntentFor(sig strategy.Signal) (optionpicker.CondorIntent, bool) {
	var sCE, lCE, sPE, lPE int64
	for _, l := range sig.Legs {
		switch {
		case l.OptionType == "CE" && l.Short:
			sCE = l.Strike
		case l.OptionType == "CE":
			lCE = l.Strike
		case l.OptionType == "PE" && l.Short:
			sPE = l.Strike
		default:
			lPE = l.Strike
		}
	}
	if sCE == 0 || sPE == 0 || lCE <= sCE || lPE >= sPE {
		return optionpicker.CondorIntent{}, false
	}
	return optionpicker.CondorIntent{Strategy: sig.StrategyName, ShortCEAtLeast: sCE, ShortPEAtMost: sPE,
		MaxShortDelta: 0.25, WingWidth: lCE - sCE, MinDTE: 1}, true
}

type pickerDecision struct {
	Strategy string  `json:"strategy"`
	Mode     string  `json:"mode"`
	Result   string  `json:"result"` // picked / refused
	Reason   string  `json:"reason,omitempty"`
	Strike   int64   `json:"strike,omitempty"`
	Symbol   string  `json:"symbol,omitempty"`
	Token    string  `json:"token,omitempty"`
	Delta    float64 `json:"delta,omitempty"`
	IV       float64 `json:"iv,omitempty"`
	Bid      int64   `json:"bid,omitempty"`
	Ask      int64   `json:"ask,omitempty"`
	TS       string  `json:"ts"`
}

type pickerDecisions struct {
	mu   sync.Mutex
	last map[string]pickerDecision
}

func (svc *Service) recordPickerDecision(d pickerDecision) {
	svc.pickerDec.mu.Lock()
	if svc.pickerDec.last == nil {
		svc.pickerDec.last = map[string]pickerDecision{}
	}
	svc.pickerDec.last[d.Strategy] = d
	svc.pickerDec.mu.Unlock()
	log.Printf("[stratengine] 🎯 picker %s %s: %s %s %s", d.Mode, d.Strategy, d.Result, d.Symbol, d.Reason)
}

func (svc *Service) lastPickerDecision(strategy string) pickerDecision {
	svc.pickerDec.mu.Lock()
	defer svc.pickerDec.mu.Unlock()
	return svc.pickerDec.last[strategy]
}

func decisionFor(in string, mode string, p optionpicker.Pick, err error, now time.Time) pickerDecision {
	d := pickerDecision{Strategy: in, Mode: mode, TS: now.UTC().Format(time.RFC3339)}
	if err != nil {
		d.Result, d.Reason = "refused", err.Error()
		return d
	}
	d.Result = "picked"
	d.Strike, d.Symbol, d.Token = p.Strike, p.Symbol, p.Token
	d.Delta, d.IV, d.Bid, d.Ask = math.Round(p.Delta*1000)/1000, p.IV, p.Quote.Bid, p.Quote.Ask
	return d
}

func (svc *Service) pickEntry(sig *strategy.Signal, now time.Time) (bool, error) {
	if svc.picker == nil || svc.cfg.PickerMode == "off" {
		return false, nil
	}
	in, ok := svc.intentFor(*sig)
	if !ok {
		return false, nil
	}
	p, _, err := svc.picker.PickSingle(in, now)
	svc.recordPickerDecision(decisionFor(sig.StrategyName, svc.cfg.PickerMode, p, err, now))
	if svc.cfg.PickerMode != "on" {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("picker: %w", err)
	}
	sig.Strike, sig.FNOToken, sig.FNOSymbol = p.Strike, p.Token, p.Symbol
	return true, nil
}

func (svc *Service) pickBasket(sig *strategy.Signal, now time.Time) (bool, error) {
	if svc.picker == nil || svc.cfg.PickerMode == "off" || sig.Action != strategy.ActionBuy {
		return false, nil
	}
	in, ok := svc.condorIntentFor(*sig)
	if !ok {
		return false, nil
	}
	cp, _, err := svc.picker.PickCondor(in, now)
	svc.recordPickerDecision(decisionFor(sig.StrategyName, svc.cfg.PickerMode, cp.ShortCE, err, now))
	if svc.cfg.PickerMode != "on" {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("picker: %w", err)
	}
	legs := map[bool]map[string]optionpicker.Pick{
		true:  {"CE": cp.ShortCE, "PE": cp.ShortPE},
		false: {"CE": cp.LongCE, "PE": cp.LongPE},
	}
	for i := range sig.Legs {
		p := legs[sig.Legs[i].Short][sig.Legs[i].OptionType]
		sig.Legs[i].Strike, sig.Legs[i].Token, sig.Legs[i].Symbol = p.Strike, p.Token, p.Symbol
	}
	return true, nil
}

// runPicker feeds nothing itself: ticks arrive via tickRouterLoop.
func (svc *Service) runPicker(ctx context.Context) {
	if svc.picker == nil {
		return
	}
	svc.orderExecutor.SetQuoteSource(svc.picker.Quotes())
	svc.picker.Run(ctx, markethours.IsMarketOpen)
}
```

Notes for the implementer:
- `optionTypeFor`, `nearestStrike`, `deltaBand`, `ladderStrikeStep`, `normIV`, `isSRSignal`, `greeksFor` already exist in `stratengine`.
- `Nifty50Range.Name()` honours `NameOverride`, so match by the running instance's name, never a literal. The RANGE test must set `svc.nifty50RangeStrategy = strategy.NewNifty50Range(1)` (as `range_state_test.go` does).
- `strategy.LegLongCE` etc. — use the leg constants defined in `internal/strategy` (see `nifty50_range_ic.go:288-291`).

- [ ] **Step 5: Wire into service** (`service.go`)
  - Fields: `picker *optionpicker.Picker` and `pickerDec pickerDecisions`.
  - In the constructor, after `svc.orderExecutor` is set: `svc.picker = svc.newPicker()`.
  - In `Run` next to the other loops: `go svc.runPicker(ctx)`.
  - In `tickRouterLoop` after `svc.orderExecutor.UpdateLTP(tick)`: `if svc.picker != nil { svc.picker.OnTick(tick) }`.

- [ ] **Step 6: Entry path** (`option_select.go`, top of `resolveEntryStrike`):

```go
	if decided, err := svc.pickEntry(sig, now); decided {
		return err
	}
```

`resolveEntryStrike` is only called for BUYs with `sig.Strike > 0` (service.go:615). SR and RANGE both set `Strike`, so both reach it. In `on` mode a successful pick sets `FNOToken`, and the caller's remaining code uses it as today.

- [ ] **Step 7: Basket path** (`legs.go`, `handleBasketSignal`), before `legs, err := svc.expandLegSignals(sig, now)`:

```go
	pickerDecided := false
	if sig.Action == strategy.ActionBuy {
		decided, err := svc.pickBasket(&sig, now)
		if decided && err != nil {
			cancel(err.Error())
			return
		}
		pickerDecided = decided
	}
```

and change the quality check guard to `if sig.Action == strategy.ActionBuy && svc.cfg.RangeDeltaGuard && !pickerDecided {`. In `expandLegSignals` skip resolution for legs that already carry a token:

```go
		for i := range legs {
			if legs[i].Token != "" {
				continue // chosen by the option picker
			}
			info, err := res.ResolveStrike(now, legs[i].Strike, legs[i].OptionType)
```

- [ ] **Step 8: Subscription mode** (`option_select.go` / `service.go:1270`): rename the body of `publishFNOSubscription` into

```go
func (svc *Service) publishFNOSubscriptionMode(ctx context.Context, mode int, tokens ...string)
```

adding `"mode": mode` to the published JSON, and make the old function delegate:

```go
// publishFNOSubscription subscribes option tokens; SnapQuote when the picker
// runs (every option needs bid/ask), else Quote as before.
func (svc *Service) publishFNOSubscription(ctx context.Context, tokens ...string) {
	mode := 2
	if svc.picker != nil {
		mode = smartSnapQuote
	}
	svc.publishFNOSubscriptionMode(ctx, mode, tokens...)
}
```

- [ ] **Step 9: Run** `cd backend && go build ./... && go vet ./internal/stratengine/ && go test -race ./internal/stratengine/ ./internal/optionpicker/ ./internal/orderexec/` → PASS.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/stratengine/ backend/internal/optionpicker/picker.go
git commit -m "feat(stratengine): global option picker in shadow/on mode for SR, RANGE and IC"
```

---

### Task 13: Picker on the dashboard

**Files:**
- Modify: `backend/internal/stratengine/sr_strike_view.go` (`strikeSelView`, `refreshStrikeSel`, `stampStrikeSel`)
- Modify: `backend/internal/stratengine/sr_strike_view_test.go`
- Modify: `frontend/src/types/strikesel.ts`, `frontend/src/components/signals/FnoInstrumentsTab.tsx`, `frontend/src/components/signals/__tests__/fnoInstruments.test.ts`

**Interfaces:**
- Consumes: `svc.picker.Status(now)`, `svc.pickerDec.last`, `pickerDecision` (Task 12).
- Produces (JSON on `pub:strikesel`): `picker: { mode, chain_at, chain_error?, streamed, spot, rules: {max_spread_pct, max_quote_age_s, max_chain_age_s}, decisions: pickerDecision[] }`.

- [ ] **Step 1: Write the failing backend test** (append to `sr_strike_view_test.go`)

```go
func TestStrikeSelIncludesPickerStatusAndDecisions(t *testing.T) {
	var sent []string
	svc := strikeSelSvc(&fakeGreeks{chain: []orderexec.OptionContract{contract(22700, "CE", 0.52)}}, &sent)
	svc.cfg.PickerMode, svc.cfg.PickMaxSpreadPct, svc.cfg.PickMaxQuoteAge, svc.cfg.PickMaxChainAge = "shadow", 2, 3*time.Second, 2*time.Minute
	svc.picker = testPicker(t, svc)
	svc.recordPickerDecision(pickerDecision{Strategy: "NIFTY50_SR", Mode: "shadow", Result: "picked", Symbol: "NIFTY06OCT2622700CE", TS: "x"})
	svc.refreshStrikeSel(context.Background(), pickerNow)
	v := lastStrikeSel(t, sent)
	if v.Picker == nil || v.Picker.Mode != "shadow" || v.Picker.Streamed == 0 || v.Picker.Rules.MaxSpreadPct != 2 ||
		len(v.Picker.Decisions) != 1 || v.Picker.Decisions[0].Symbol != "NIFTY06OCT2622700CE" {
		t.Fatalf("picker view = %+v", v.Picker)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`v.Picker undefined`).

- [ ] **Step 3: Implement** — in `sr_strike_view.go` add:

```go
type pickerRulesView struct {
	MaxSpreadPct  float64 `json:"max_spread_pct"`
	MaxQuoteAgeS  float64 `json:"max_quote_age_s"`
	MaxChainAgeS  float64 `json:"max_chain_age_s"`
}

type pickerView struct {
	Mode       string           `json:"mode"`
	ChainAt    string           `json:"chain_at,omitempty"`
	ChainError string           `json:"chain_error,omitempty"`
	Streamed   int              `json:"streamed"`
	Spot       int64            `json:"spot"`
	Rules      pickerRulesView  `json:"rules"`
	Decisions  []pickerDecision `json:"decisions"`
}
```

field `Picker *pickerView \`json:"picker,omitempty"\`` on `strikeSelView`, and in `stampStrikeSel` (caller holds `strikeSel.mu`; `pickerDec.mu` is a different lock, no ordering issue since `recordPickerDecision` never takes `strikeSel.mu`):

```go
	v.Picker = nil
	if svc.picker != nil {
		st := svc.picker.Status(now)
		pv := &pickerView{Mode: svc.cfg.PickerMode, Streamed: st.Streamed, Spot: st.Spot, ChainError: st.ChainErr,
			Rules: pickerRulesView{MaxSpreadPct: svc.cfg.PickMaxSpreadPct, MaxQuoteAgeS: svc.cfg.PickMaxQuoteAge.Seconds(), MaxChainAgeS: svc.cfg.PickMaxChainAge.Seconds()}}
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
```

(import `sort`). Also start `strikeSelLoop` when the picker exists even if SR is disabled: in `service.go` change `if svc.cfg.SREnabled {` around `go svc.strikeSelLoop(ctx)` to `if svc.cfg.SREnabled || svc.picker != nil {`.

- [ ] **Step 4: Run** `cd backend && go test ./internal/stratengine/` → PASS.

- [ ] **Step 5: Write the failing frontend test** (append to `fnoInstruments.test.ts`)

```ts
import { decisionLine } from '../FnoInstrumentsTab';

describe('picker decisions', () => {
    it('formats picked and refused decisions', () => {
        expect(decisionLine({ strategy: 'NIFTY50_SR', mode: 'shadow', result: 'picked', symbol: 'NIFTY06OCT2622700CE', delta: 0.52, bid: 20280, ask: 20320, ts: '' }))
            .toBe('picked NIFTY06OCT2622700CE · Δ 0.52 · ₹202.80 / ₹203.20');
        expect(decisionLine({ strategy: 'NIFTY50_RANGE', mode: 'on', result: 'refused', reason: 'no CE passes (dte 6; rejected spread 3)', ts: '' }))
            .toBe('refused: no CE passes (dte 6; rejected spread 3)');
    });
});
```

- [ ] **Step 6: Implement frontend**

`types/strikesel.ts` — add:

```ts
export interface PickerDecision {
    strategy: string;
    mode: string;
    result: 'picked' | 'refused' | string;
    reason?: string;
    strike?: number;
    symbol?: string;
    token?: string;
    delta?: number;
    iv?: number;
    bid?: number; // paise
    ask?: number; // paise
    ts: string;
}

export interface PickerView {
    mode: string;
    chain_at?: string;
    chain_error?: string;
    streamed: number;
    spot: number;
    rules: { max_spread_pct: number; max_quote_age_s: number; max_chain_age_s: number };
    decisions?: PickerDecision[];
}
```

and `picker?: PickerView;` on `StrikeSelView`.

`FnoInstrumentsTab.tsx` — add the exported formatter and a card rendered first when `view.picker` exists:

```tsx
const paise = (v?: number) => (v && v > 0 ? `₹${(v / 100).toFixed(2)}` : '—');

export function decisionLine(d: PickerDecision): string {
    if (d.result !== 'picked') return `refused: ${d.reason ?? ''}`;
    return `picked ${d.symbol ?? d.strike} · Δ ${num(d.delta ?? NaN, 2)} · ${paise(d.bid)} / ${paise(d.ask)}`;
}

function PickerCard({ p }: { p: PickerView }) {
    return (
        <div className="fno-atm-card">
            <div className="fno-atm-header">
                <Target size={16} />
                <span>Global option picker · {p.mode}</span>
                <span className="fno-badge resolved">{p.streamed} streamed</span>
            </div>
            <div className="fno-atm-body">
                <div className="fno-atm-stat"><span className="fno-atm-label">Greeks (chain)</span>
                    <span className="fno-atm-value fno-param">{p.chain_error ? `error: ${p.chain_error}` : fmtTime(p.chain_at ?? '')}</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Max spread</span>
                    <span className="fno-atm-value fno-param">{num(p.rules.max_spread_pct, 1)}% of mid</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Max quote age</span>
                    <span className="fno-atm-value fno-param">{p.rules.max_quote_age_s}s</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Max greeks age</span>
                    <span className="fno-atm-value fno-param">{p.rules.max_chain_age_s}s</span></div>
            </div>
            <div className="fno-inst-body">
                {(p.decisions ?? []).length === 0 && <div className="fno-inst-row"><span className="fno-na">No entry signal yet</span></div>}
                {(p.decisions ?? []).map(d => (
                    <div key={d.strategy} className="fno-inst-row">
                        <span className="fno-inst-label">{d.strategy} · {fmtTime(d.ts)}</span>
                        <span className="fno-inst-value">{decisionLine(d)}</span>
                    </div>
                ))}
            </div>
        </div>
    );
}
```

Import `PickerDecision, PickerView` from the types, and render `{view.picker && <PickerCard p={view.picker} />}` as the first child of `.fno-instruments-wrap`.

- [ ] **Step 7: Run** `cd frontend && npx tsc -b && npm test` → PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/stratengine/ frontend/src/
git commit -m "feat: show global option picker status and decisions on the FNO tab"
```

---

### Task 14: Verify end to end in staging

**Files:** none (verification only; fix defects in the owning task's files and re-run that task's tests).

- [ ] **Step 1:** `cd backend && go build ./... && go vet ./... && go test -race ./...` → all PASS.
- [ ] **Step 2:** `cd frontend && npm run build && npm test` → PASS.
- [ ] **Step 3:** Run the stack in staging: `./scripts/air_staging.sh` (repo root) with `STRAT_PICKER_MODE=shadow` in `.env.staging`, and `cd frontend && npm run dev`.
- [ ] **Step 4:** Check logs: `[optionpicker] option chain:` lines every 15 s (or the rate-limit backoff line), `[optionpicker] universe around ... +N contracts`, and mdengine `dynamically subscribed mode=3`.
- [ ] **Step 5:** Open Signals → FNO instruments: the "Global option picker · shadow" card shows streamed > 0 and a greeks time within the last 15 s.
- [ ] **Step 6:** Record the result (pass/fail per check) in the PR description. Do not change `STRAT_PICKER_MODE` to `on` in production — that is Task 15, after the user reviews shadow decisions from real sessions.

---

### Task 15 (after shadow review, user-gated): cut over and delete the old selection code

**Run only after the user has compared shadow decisions against the old logic for a few production sessions and asked to switch.**

**Files:**
- Modify: `backend/internal/stratengine/config.go` (default `STRAT_PICKER_MODE` → `"on"`)
- Delete code: `pickSRStrike`, `selectSRContract`, `evalSRContracts`, `srCostOK`, `srPremium` (`sr_strike.go`); `deltaChecked`, `entryQualityCheck`, `basketQualityCheck` (`delta_guard.go`); `refreshStrikeLadder`, `ladderTokens` (`option_select.go`); `subscribeSRExpiryLadder` (`sr_strike_view.go`); the `tickRouterLoop` call to `refreshStrikeLadder`; the live greek pick in `refreshStrikeSel` (keep the picker view).
- Delete their tests: `sr_strike_test.go`, the delta-guard tests in `delta_guard_test.go` that exercise deleted functions, the ladder tests in `option_select_test.go`, the SR expiry-ladder and live-pick tests in `sr_strike_view_test.go`.

- [ ] **Step 1:** Change the default, delete the functions and their tests listed above.
- [ ] **Step 2:** In `resolveEntryStrike`, with the picker always deciding, reduce the function to the `pickEntry` call plus a refusal when it did not decide (`return fmt.Errorf("no option picker for %s", sig.StrategyName)`); in `handleBasketSignal` remove the `basketQualityCheck` branch.
- [ ] **Step 3:** `cd backend && go build ./... && go vet ./... && go test -race ./...` → PASS; `cd frontend && npm test` → PASS (the FNO tab's SR live-pick cards disappear when `call`/`put` are absent; confirm they render nothing rather than errors).
- [ ] **Step 4:** Commit:

```bash
git add -A backend/internal/stratengine
git commit -m "refactor(stratengine): option picker decides; remove per-strategy strike selection"
```
