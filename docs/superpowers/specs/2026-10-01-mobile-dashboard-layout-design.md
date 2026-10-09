# Mobile Dashboard Layout — UX/UI Design

- **Date:** 2026-10-01
- **Update 2026-10-09:** The range strategy was removed (commit c03309c). `RangePanel` / `components/range/` below are now `SRPanel` / `components/sr/`. Phone portrait chrome is redesigned in `2026-10-09-mobile-native-app-ux-design.md`.
- **Status:** Implemented (P1–P4); same-bar entry+exit badge merge deferred — entries draw below price and exits above, so they do not collide
- **Scope:** Dashboard route (`/`) at phone widths (≤ 640px). Signals and Health tabs get light notes only.
- **Frontend root:** `frontend/src/`. All paths below are relative to it unless stated.

---

## 1. Goal and principles

The phone dashboard has one job: show the live chart, and tell the trader at a glance what the range strategy is doing. Today the chart gets about 370px of an 844px screen, and the strategy panel sits below the fold, partly hidden behind the bottom nav.

Principles:

1. **The chart is the hero.** It takes all height not used by fixed chrome. The dashboard page never scrolls.
2. **Strategy state is always visible.** A one-line summary is always on screen. Details open in a bottom sheet on demand.
3. **One-thumb reach.** Frequent controls (timeframe, strategy sheet, tabs) sit in the lower two thirds or in the bottom bar.
4. **Touch targets of at least 40px tall, 44px preferred.**
5. **No new colors.** Use the existing `--sg-*` tokens in `styles/global.css`. Use `tabular-nums` for every number.
6. **Every pixel of chrome must earn its place.** Remove controls that do nothing useful on a phone.

---

## 2. Audit of the current layout

Reference screenshot: 390px-wide viewport, dashboard tab, NIFTY 1m, market open.

| # | Problem | Where |
|---|---|---|
| A1 | **Chart starved of height.** Vertical stack: header 48 + layout bar ~30 + two-row toolbar ~90 + chart `max(340px, 58dvh)` + Range panel + nav 56. The page scrolls inside a fixed shell. | `pages/DashboardPage.module.css:63-82`, `App.module.css` (`.shellFixed`) |
| A2 | **Strategy panel cut off.** "WATCHING" text sits behind the bottom nav. Position and footer need a scroll to reach. | `pages/DashboardPage.tsx` (`<RangePanel />` after grid) |
| A3 | **"Charts 1 / 2 / 4" selector is useless on a phone.** Layouts 2 and 4 stack vertically at 320px each (`grid-auto-rows: 320px`), so they only add scroll. Buttons use `padding: 2px 8px` — far below a usable target. | `pages/DashboardPage.tsx:26-42`, `DashboardPage.module.css:33-47, 76-81` |
| A4 | **Raw instrument id.** The select shows `NSE:99926000`, not "NIFTY 50". No token-to-name map exists. | `components/chart/TradingChart.tsx:117-127` |
| A5 | **Range panel is desktop-shaped.** `.stats` is a wrapping flex row, so six stats fall into a ragged 5 + 1 layout. Support, Resistance and Width show "—" but still take full cells. | `components/range/RangePanel.module.css:66-76`, `RangePanel.tsx` |
| A6 | **Two header pills for two booleans.** "Market open" and "Live" each take a pill. On narrow phones (360px) they crowd the brand. | `components/layout/Header.tsx:79-89` |
| A7 | **Small timeframe buttons.** 30px tall on phones. | `components/chart/Chart.module.css:299` |
| A8 | **Marker clutter.** Entry/exit "P" badges overlap candles at 1m density. Compact mode is decided once with `matchMedia(...).matches` and does not update on resize or rotate. | `components/chart/hooks/useChartMarkers.ts:222`, `tradeMarkersPrimitive.ts` |
| A9 | **No landscape treatment.** In landscape (height ≈ 390px) the chrome leaves almost no chart. | — |

---

## 3. Target layout — portrait (390 × 844)

```
┌──────────────────────────────────────┐
│ ⚡ TradingPulse        ● Open · Live │  Header            44px
├──────────────────────────────────────┤
│ NIFTY 50 ▾                 22,568.10 │  Instrument row    52px
│ NSE · 99926000        +13.60 (+0.06%)│
├──────────────────────────────────────┤
│ [ 1m ][ 2m ][ 3m ][ 5m ][ 1h ]  [≡ 2]│  Timeframe row     44px
├──────────────────────────────────────┤
│ O 22556.35 H 22568.15 L 22555.50 C … │  (legend overlays chart)
│                                      │
│                                      │
│              CHART                   │  flex: 1
│          (≈ 520–560px)               │
│                                      │
│                                      │
├──────────────────────────────────────┤
│ ═══                                  │  drag handle
│ Trending  Flat  ADX 37.5  RSI 44  ⌃  │  Strategy peek     56px
│                         Day −6.4 pts │
├──────────────────────────────────────┤
│   ▦ Chart     ▥ Signals    ∿ Health  │  Bottom nav 56px + safe area
└──────────────────────────────────────┘
```

### Height budget

| Block | Today | Target |
|---|---|---|
| Header | 48 | 44 |
| Layout bar (Charts 1/2/4) | ~30 | 0 (hidden) |
| Chart toolbar | ~90 | 52 + 44 = 96 |
| Chart | ~370 (58dvh) | **flex: 1 → ~520–560** |
| Strategy | below fold, scrolls | 56 peek, always visible |
| Bottom nav | 56 + safe area | 56 + safe area |
| Page scroll | yes | **no** |

The toolbar grows a little, but the layout bar is gone and the chart takes everything left. The net chart gain is about 150–190px.

### Rules

- At ≤ 640px the dashboard page uses `overflow: hidden`. The grid is `flex: 1 1 auto; min-height: 0`.
- The "Charts 1/2/4" bar is hidden at ≤ 640px. The page renders one chart. The saved desktop layout in `dashboardLayout_v1` is **not** changed, so a desktop user keeps their 2- or 4-chart choice.
- The strategy peek strip is pinned between the chart and the bottom nav. It is part of the flex column, not an overlay, so it never hides chart content.

---

## 4. Component specs

### 4.1 Header (44px)

- Left: `Zap` 16px + "TradingPulse", 15px / 600, `--sg-text`.
- Right: **one combined status pill**, 28px tall, 0 10px padding, font 12px / 500.
  - Text is the market state; the dot is the feed state.

| Market | Feed | Pill text | Dot |
|---|---|---|---|
| Open | Live | `Open · Live` | `--sg-profit`, pulsing |
| Open | Reconnecting | `Open · Reconnecting` | `--sg-loss` |
| Closed | Live | `Closed` | `--sg-profit`, static |
| Holiday | any | `Holiday` | feed color |
| Weekend | any | `Weekend` | feed color |
| any | Connecting | `Connecting…` | `--sg-muted` |

- Pill background uses the matching `-soft` token for the market state (`--sg-profit-soft` open, `--sg-warn-soft` holiday, `--sg-line-soft` closed).
- Tap the pill to show a small popover with the full text, including "Opens Mon 09:15" (today's `pillSub`, which is hidden on mobile).
- The existing `ReconnectBanner` stays. It is the loud signal; the pill is the quiet one.

### 4.2 Instrument row (52px)

- Left: instrument button, at least 44px tall.
  - Line 1: friendly name, "NIFTY 50", 15px / 600, then `ChevronDown` 14px.
  - Line 2: `NSE · 99926000`, 11px, `--sg-faint`.
  - Tap opens a native `<select>` (keep native for accessibility and OS pickers). Option text uses the friendly name.
  - Unknown tokens fall back to the raw id.
- Right, right-aligned:
  - Last price, 18px / 600, `tabular-nums`, `--sg-text`.
  - Change, 12px, `--sg-profit` or `--sg-loss`, with sign and percent.
- Padding 0 12px. Bottom border `--sg-line-soft`.

### 4.3 Timeframe row (44px)

- Segmented control, `flex: 1`, height 40px, radius `--sg-r-ctrl`, background `--sg-raised`.
- Each segment `flex: 1`, font 13px / 600. Active segment: `--sg-hover` background, `--sg-text`. Inactive: `--sg-muted`.
- Right: indicators button, 44 × 40px, `SlidersHorizontal` 18px, count badge on the top-right corner (existing `.toolCount`).
- Keep `role="radiogroup"` / `role="radio"` / `aria-checked`.

### 4.4 Chart

- Fills remaining height. The existing ResizeObserver in `useChartInit.ts` handles the size change.
- OHLC legend: one line, 11px, `tabular-nums`. On narrow screens (< 380px) drop the "O" and "H/L" labels' values to show only `C` and change, or allow the line to truncate with ellipsis. Indicator values go on a second line only when indicators are active.
- Price scale and time scale font 11px.
- Touch drag and pinch zoom stay as today. No gesture on the chart opens the sheet.

### 4.5 Trade markers

- Compact mode (side letter only) already exists. Fix: replace the one-time `matchMedia(...).matches` with a listener so rotate and resize update it.
- When an entry and exit land on the same bar, show one combined badge (`↗↘ P`) instead of two.
- When bar spacing is below ~6px (zoomed out), draw a 6px dot in the side color instead of a badge.
- Badge minimum hit area is not needed — markers are not interactive on mobile.

### 4.6 Strategy peek strip (56px)

```
═══                                       (drag handle, 32×4px, --sg-line)
[Trending]  Flat   ADX 37.5   RSI 44   ⌃
                           Day −6.4 pts
```

- Background `--sg-surface`, top border `--sg-line`, top radius `--sg-r-panel`.
- Content, left to right, by priority. Drop from the right when space runs out:
  1. Regime pill (`Range` info, `Trending` warn, `Warming up` muted) — existing classes.
  2. Position: `Flat` (`--sg-muted`), or `CALL +12.4` / `PUT −3.0` in profit/loss colors. **Position always wins space over stats.**
  3. `ADX 37.5`
  4. `RSI 44`
  5. `Day −6.4 pts`
- If "Watching" is live (`watch.kind === 'pending'` or a ready flag), replace stats with the watch text in `--sg-warn`, one line, ellipsis. This is the moment the trader needs to look.
- The whole strip is a button (`aria-expanded`, `aria-controls` the sheet). Tap or drag up opens the sheet.
- Empty state: `Strategy · Waiting for engine…` in `--sg-muted`.

### 4.7 Strategy bottom sheet

Snap points:

- **Half:** ≈ 50dvh. Chart stays visible above it, so price and markers remain readable.
- **Full:** ≈ 90dvh.
- **Closed:** back to the peek strip.

Content order, most urgent first:

1. **Header row:** "Range strategy", regime pill, `Paper` pill, `as of 11:59 IST` (right).
2. **Position** (only when in a position): side pill, kind, strike `22550 CE`, P&L points large (18px). Below: Entry / Stop / Target in a 3-column grid, with distance to stop and target as sub-lines.
3. **Watching:** full text, wrapping. Live state in `--sg-warn`.
4. **Situation:** fixed **3-column grid** (`grid-template-columns: repeat(3, minmax(0, 1fr))`), row gap 12px.
   - Row 1: 15m ADX (sub `range < 30`), 5m RSI, Day move.
   - Row 2: Support, Resistance, Width (only when a range exists).
   - When there is no range, collapse row 2 into one muted line: `No range levels yet`. Do not show three "—" cells.
5. **Footer as chips:** `Trades 0/3`, `Entries on 5m closes`, `Flat by 15:10`, and mode chips `Breakout` / `Flag` / `Mean rev.` (on = `--sg-profit-soft`, off = `--sg-line-soft` with `--sg-faint` text, no strikethrough).
6. When not in a position, Position shows a single `Flat` line at the bottom of the Situation block instead of its own card.

Behavior:

- Drag only on the handle and header row, so the content can scroll on its own at full height.
- Close by drag down, tap on the dimmed area above (at full only; at half there is no backdrop so the chart stays usable), or `Esc`.
- `role="dialog"`, `aria-modal="true"` at full; `aria-modal="false"` at half. Focus moves into the sheet on open and back to the strip on close.
- Motion: 200ms ease-out slide. Under `prefers-reduced-motion: reduce`, no slide, instant snap.
- Remember the last snap point per session (`sessionStorage`, wrapped in try/catch).

### 4.8 Bottom nav

Keep `components/layout/MobileNav.tsx` as is: 56px, safe-area padding, accent icon on the active tab, `SignalBadge` on Signals. Only change: label font 11px and active label 600 weight, so the active tab reads without relying on color alone.

---

## 5. Landscape (height ≤ 500px)

```
┌────────────────────────────────────────────────────────────────┐
│ NIFTY 50 ▾  22,568.10 +13.60  [1m][2m][3m][5m][1h] [≡]   ● Live│ 44px
├────────────────────────────────────────────────────────────────┤
│                                                                │
│                         CHART (full)                           │
│                                                                │
├────────────────────────────────────────────────────────────────┤
│  Chart   Signals   Health                                      │ 44px
└────────────────────────────────────────────────────────────────┘
```

- Header brand hidden; instrument, price, timeframe and status merge into one 44px row.
- Peek strip hidden. A small regime/position chip sits in the top-right of the chart; tap opens the sheet as a right-side panel (40% width).
- Bottom nav shrinks to 44px, labels beside icons.
- Media query: `@media (max-width: 940px) and (max-height: 500px) and (orientation: landscape)`.

---

## 6. Typography and spacing scale (mobile)

| Use | Size / weight |
|---|---|
| Captions, labels, sub-lines | 11px / 500 |
| Body, chips, pills | 12–13px / 500 |
| Section titles, instrument name | 15px / 600 |
| Prices, P&L hero | 18px / 600, `tabular-nums` |

Spacing steps: 4, 8, 12, 16px. Side gutter 12px. Card radius `--sg-r-panel`, control radius `--sg-r-ctrl`.

Note: `html { font-size: 13px }` at ≤ 640px (`styles/global.css`). Specs above are in px on purpose; when implementing, convert with that base or set px directly in the mobile media queries.

---

## 7. States

| State | Header pill | Instrument row | Chart | Peek strip |
|---|---|---|---|---|
| Loading config | `Connecting…` | skeleton bar | "Loading…" | hidden |
| WS disconnected | `… · Reconnecting` (loss dot) | last price dimmed to `--sg-muted` | frozen, `ReconnectBanner` shows | stats dimmed |
| No candles | normal | no price | "Waiting for market data for NIFTY 50…" | normal |
| Strategy warming | normal | normal | normal | `Warming up` pill, `Waiting for 15m ADX…` |
| In position | normal | normal | entry/stop/target lines (existing `useRangeLines`) | position first, colored P&L |
| Market closed | `Closed` | normal | normal | `Flat` · `Market closed` |

---

## 8. Accessibility

- `--sg-faint` (#5d6778) on `--sg-raised` (#1b2330) is below 4.5:1. Use it only for non-essential captions; never for values, the watch text or P&L. Use `--sg-muted` for anything the trader must read.
- Profit/loss always carries a sign (`+` / `−`), not only color.
- All controls keep visible focus via `--sg-ring`.
- Sheet: dialog semantics, focus management, `Esc` to close (see 4.7).
- Respect `prefers-reduced-motion` for the sheet and the live-dot pulse (already handled for the dot in `Header.module.css`).

---

## 9. Signals and Health tabs (notes only)

- Use the same 44px header with the combined status pill.
- Same 40px minimum targets for filters and tabs.
- These pages may scroll; only the dashboard is fixed.
- Separate design pass later.

---

## 10. Implementation plan

Each phase ships on its own and is visible on a phone.

### P1 — CSS layout fixes (no new components)

- `pages/DashboardPage.module.css`: at ≤ 640px remove page scroll, give `.grid` `flex: 1 1 auto; min-height: 0`, hide `.layoutBar`.
- `pages/DashboardPage.tsx`: at ≤ 640px render one chart regardless of saved count (do not write back to `dashboardLayout_v1`).
- `components/chart/Chart.module.css`: timeframe buttons 40px tall, instrument row 52px, legend 11px.
- `components/layout/Header.tsx` + `Header.module.css`: combined status pill on mobile, header 44px.
- Temporary: `RangePanel` stays below the chart but with `max-height` and its own scroll, until P3.

### P2 — Friendly instrument names

- Add a small `TOKEN_LABEL` map (for example `'NSE:99926000' → 'NIFTY 50'`) in `utils/helpers.ts`, with raw-id fallback.
- Use it in the `<select>` options and the caption in `components/chart/TradingChart.tsx`.

### P3 — Strategy peek strip and bottom sheet

- New `hooks/useMediaQuery.ts` (none exists today).
- New `components/range/RangePeek.tsx` and `components/range/RangeSheet.tsx`.
- Extract the body of `RangePanel.tsx` into a shared piece so desktop keeps `RangePanel` and mobile uses the sheet. Reuse `useRangeStore` and `rangeFormat.ts` (`watching`, `points`, `rupees`, `hhmm`).
- 3-column Situation grid and "No range levels yet" collapse.

### P4 — Markers and landscape

- `components/chart/hooks/useChartMarkers.ts`: use `useMediaQuery` instead of the one-time check; combined same-bar badge; dot fallback at small bar spacing (`tradeMarkersPrimitive.ts`).
- Landscape media query and merged header row.

---

## 11. Acceptance checks

Test at 360 × 740, 390 × 844, 412 × 915 (portrait) and 844 × 390 (landscape), in Chrome device mode and on one real phone.

- [ ] Dashboard page does not scroll.
- [ ] Chart height is at least 60% of viewport height in portrait.
- [ ] Strategy regime and position are visible without any tap or scroll.
- [ ] Nothing is hidden behind the bottom nav (check with an iOS safe-area inset).
- [ ] Every tappable control is at least 40px tall.
- [ ] Instrument shows "NIFTY 50", not `NSE:99926000`.
- [ ] Rotate portrait ↔ landscape: markers and layout update without reload.
- [ ] Sheet opens, snaps, closes by drag, tap and `Esc`; focus returns to the strip.
- [ ] Desktop (≥ 641px) layout unchanged, including saved 2/4 chart layouts.
