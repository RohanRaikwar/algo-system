# Signal Tab Frontend Steps

## Goal
Add three frontend sections under Signals:
1. `Signal Log` (existing table)
2. `Live Order Status` (open order name/type + buy price + current price)
3. `Single Event Price` (buy price and exit price per completed event)

## Architecture Notes (from referenced `architecture_patterns.md`)
- Keep clear structure and logical separation.
- Use consistent naming for data models and UI components.
- Keep parsing/derivation logic separate from rendering components.
- Add only required data transformations; avoid mixing business logic in JSX.

## Step-by-Step Plan

### 1. Add Signal Sub-Tab State in Signals Page
- In `frontend/src/pages/SignalsPage.tsx`, add tab state:
  - `LOG`
  - `LIVE`
  - `EVENT_PRICE`
- Render a tab switcher above content.
- Keep current table under `LOG` tab.

### 2. Add Derived Data Helper (No API change)
- Create a helper file in `frontend/src/components/signals/` (example: `signalAnalytics.ts`).
- Add helper functions:
  - infer order side (`CALL` / `PUT`) from strategy/reason text.
  - extract display price from signal payload or reason text.
  - build open orders and completed entry-exit events from signal sequence.
- Keep this helper pure (no React hooks).

### 3. Live Order Status Section
- Under `LIVE` tab, add a table with columns:
  - Order Name (strategy)
  - Side (`CALL`/`PUT`)
  - Instrument (`exchange:token`)
  - Buy Price
  - Current Price
  - Entry Time
- Compute current price using latest candle from `useCandleStore` for selected TF/token.
- Show fallback `--` if live price is unavailable.

### 4. Single Event Price Section
- Under `EVENT_PRICE` tab, add completed event table with:
  - Order Name
  - Side
  - Instrument
  - Buy Price + Buy Time
  - Exit Price + Exit Time
  - Move (Exit - Buy)
- Build these rows by pairing `BUY` and `EXIT` signals by `strategy + exchange + token` in chronological order.

### 5. Keep Existing Features Intact
- Keep current filters (`strategy`, `action`, `token`) for the `Signal Log` tab.
- Keep existing stats cards (`Total`, `Today`, `Entries`, `Exits`).
- Do not remove current unread/audio behavior.

### 6. Styling Updates
- Extend `frontend/src/components/signals/signals.css`:
  - tab button styles
  - section summary cards
  - compact tables for `LIVE` and `EVENT_PRICE`
  - color classes for up/down/flat price move
  - responsive behavior for mobile widths

### 7. Validation
- Build frontend:
  - `cd frontend && npm run build`
- Manual checks:
  - tab switching works
  - live orders update when new BUY/EXIT arrives
  - buy/exit prices render correctly from available signal data
  - no regression in existing Signal Log table

## Acceptance Checklist
- [ ] Signal page has `Signal Log`, `Live Order Status`, and `Single Event Price` tabs.
- [ ] Live section shows order name, side, buy price, and current price.
- [ ] Event section shows buy and exit prices per completed trade event.
- [ ] Existing signal filters and audio toggle still work.
- [ ] Frontend build passes.
