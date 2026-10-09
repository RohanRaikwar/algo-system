# Mobile native-app UX — design

**Status:** Implemented (2026-10-09). Differences from the draft:
- Today strip P&L uses today's realized P&L from `/api/orders/daily` (`useDailyOrders`), already loaded for the Trades view. No `usePnLStore` was needed.
- Health on phone shows Account, Session health and Settings as a segmented control (`System · Account · Sessions · Settings`, `?s=` param), not stacked sections.
- The dashboard SR peek in phone landscape is unchanged.
**Supersedes (phone portrait only):** parts of `2026-10-01-mobile-dashboard-layout-design.md`
**Scope:** frontend only, phone portrait (`(max-width: 640px) and (orientation: portrait)`). Desktop and phone landscape do not change.

The existing `PHONE_QUERY` (`max-width: 640px`) also matches small phones in landscape (e.g. 640×360). This redesign needs a new `PHONE_PORTRAIT_QUERY` in `hooks/useMediaQuery.ts`, and the new CSS uses the same media condition.

## 1. Goal

The phone build should feel like an installed app, not a website squeezed onto a small screen.

1. Give the chart as much height as possible.
2. Remove chrome that repeats or that the user rarely touches.
3. Use native mobile patterns: bottom sheets, tappable cards, OS pickers, press feedback, safe areas.

### Non-goals

- No backend change, no new Redis channel, no new REST endpoint.
- No new routes. `/`, `/signals` and `/health` stay.
- Desktop (`> 640px`) and phone landscape (`PHONE_LANDSCAPE_QUERY`) stay pixel-identical.

## 2. Current-state audit

### Dashboard (390×844)

Three bars stack above the chart:

| Bar | Height | Content | Problem |
|---|---|---|---|
| Brand bar (`components/layout/Header.tsx`) | ~48px | "TradingPulse" + "Open · Live" pill | The installed app already shows the name. Market and feed state fit in a dot. |
| Symbol row (`components/chart/TradingChart.tsx:117-145`) | ~52px | NIFTY 50 ▾, NSE caption, price, change | Keep it. |
| Timeframe row (`TradingChart.tsx:147-177`) | ~50px | 1m 2m 3m 5m 1h + indicators button | The TF changes rarely. A full row for it is waste. |

Chart overlay defects:

- The OHLC legend (`components/chart/ChartLegend.tsx`) wraps to two rows on phone. `C` drops to the second row.
- `SRSituationCard` sits at a fixed `top: 30px` (`SRSituationCard.module.css`). It assumes a one-row legend, so it collides with the wrapped legend. Its text is clipped, e.g. "SR · Trend up · 74% of day · Sideways 59.3 pts/30m".
- The card covers the newest candles at the top-left.
- The `SIDEWAYS` label of the SR box (`srBoxPrimitive.ts:56`) is hidden under both overlays.

Native-feel gaps:

- `index.html` has no `viewport-fit=cover`, so `env(safe-area-inset-top)` is always 0.
- The status bar style is `black`, not `black-translucent`.
- Buttons, pills and tabs give no press feedback.
- A long-press on chrome selects text and opens the callout menu.
- There is no haptic feedback.

The PWA base already exists: `public/manifest.webmanifest` (`display: standalone`), `public/sw.js`, icons and the apple meta tags.

### Indicators modal (390×844)

- `components/settings/SettingsModal.tsx` is pinned to the bottom on phone (`Settings.module.css:414`). It still behaves like a desktop modal: no grab handle, no swipe to close.
- It autofocuses the Period input (`SettingsModal.tsx:78`). The keyboard opens at once and covers half the screen.
- Quick-add chips, the most common action, sit at the bottom below the custom-add form.
- The SR overlay is a small desktop checkbox.

### Signals page (390×844)

`pages/SignalsPage.tsx` (483 lines) and `components/signals/signals.css`:

- The brand bar repeats here (~48px).
- The stats card uses ~190px for 5 numbers. "Total signals 234" carries no decision value.
- The "Sound on" button does nothing. `audioEnabled` in `useSignalStore` is never read.
- 9 tabs sit in a sideways-scrolling bar. About 2.5 tabs are visible. Account, Session health and Settings are off-screen, and nothing hints that they exist.
- Filters (strategy, action, token) use ~110px even when no filter is active.
- The log table has `min-width: 880px` (`signals.css:288-293`). On a 390px screen only Time, Action and Side are visible. Market, Price, P&L and Reason need a sideways scroll.
- Rows cannot be tapped. Full text exists only in `title` tooltips, and touch screens do not show tooltips.
- Live orders and Trade events tables have the same 880px problem.

## 3. Dashboard top bar

One bar, about 52px plus the safe-area top inset. It replaces the brand bar, the symbol row and the TF row.

```
┌──────────────────────────────────┐
│●NIFTY 50▾ 1m▾     22,463.00   ⚙ │
│ NSE·99926000      +146 (+0.66%)  │
├──────────────────────────────────┤
│              CHART               │
```

Left to right:

1. **Status dot.** It replaces the "Open · Live" pill.

   | State | Dot |
   |---|---|
   | Market open, feed live | green, pulsing |
   | Market open, feed connecting | green, static |
   | Holiday | amber |
   | Closed or weekend | grey |
   | Feed reconnecting | red |

   The full label goes in `aria-label` and `title`. A tap shows a 2-second toast with the full text, e.g. "Market open · Feed live". `ReconnectBanner` still announces outages.
2. **Instrument title.** `NIFTY 50 ▾` with the NSE caption under it. Same native `<select>` as today.
3. **TF chip.** A 28px pill that reads `1m ▾`. Padding gives it a 44px tap target. A transparent native `<select>` covers the chip, so a tap opens the OS picker (iOS wheel, Android list). The select uses `font-size: 16px` so iOS does not zoom.
4. **Price.** The last price, with the change and percent under it. Right-aligned. Unchanged from today.
5. **Indicators button.** Icon only (`SlidersHorizontal`), with the active-count badge. It opens the Indicators sheet (§6).

The chart gains about 100px of height.

## 4. Chart overlay rules

At most one overlay line sits on the chart on phone.

- The OHLC row stays on one line: `white-space: nowrap`, ellipsis on overflow, tabular numbers.
- The legend and the SR situation share one flex column at the top-left. No overlay uses a fixed `top` offset.
- The SR situation becomes a compact chip, e.g. `SR · Trend up` + amber `⚠ CALL/PUT: sideways` when a warning exists.
- A tap on the chip opens the SR sheet at half height.
- The full situation text (day %, sideways range, pts/30m) lives in the `SRSheet` Situation card, not on the chart.
- The `SIDEWAYS` box label must stay visible.

## 5. Native behaviors

- `index.html`: `viewport-fit=cover` and `apple-mobile-web-app-status-bar-style: black-translucent`.
- Each top bar pads with `env(safe-area-inset-top)`. The tab bar already pads with `env(safe-area-inset-bottom)`.
- App chrome (bars, tabs, chips, buttons) uses `user-select: none` and `-webkit-touch-callout: none`. Numbers in sheets and cards stay selectable.
- Buttons, chips, tabs and cards show a press tint on `:active`.
- `haptic()` calls `navigator.vibrate(10)` on TF change, tab change, sheet snap and indicator add/remove/apply. It does nothing when the API is missing (iOS Safari).
- Sheets snap with velocity: a fast flick down closes, a fast flick up opens fully. A tap on the backdrop closes.
- The tab bar shows a pill behind the active icon.
- All motion respects `prefers-reduced-motion`.

## 6. Indicators bottom sheet

The ⚙ button opens a bottom sheet with the same look as `SRSheet`.

- A grab handle, 16px top radius, a dimmed backdrop.
- Height fits the content, maximum `90dvh`.
- Close: swipe down on the handle or header, tap the backdrop, tap ✕ or press Escape.
- No autofocus on phone. The keyboard opens only when the user taps Period.
- Section order, chosen for one-thumb use:
  1. Header: title "Indicators", subtitle "Lines drawn on the 1m chart", ✕.
  2. On chart: the active list.
  3. Quick add chips.
  4. Custom add: EMA / SMA / SMMA, period, TF, Add.
  5. Overlays: SR situation as a toggle switch.
- The footer (Remove all · Cancel · Apply) is sticky with `env(safe-area-inset-bottom)` padding. When the keyboard opens, the sheet reads `visualViewport` height so the footer stays visible.
- Desktop keeps the centered modal.

## 7. Signals screen

### Information architecture

| Today (9 tabs) | Phone after |
|---|---|
| Signal log | Segment **Log** |
| Live orders | Segment **Positions** (count badge) |
| Trade events | Segment **Trades** |
| FNO instruments, Today, P&L | Segment **More**: a list; each item opens a full-screen view with a back chevron |
| Account, Session health, Settings | **Health** tab, as sections |

Desktop keeps all 9 tabs.

### Layout, top to bottom

1. **Page app bar.** Large title "Signals". On the right: status dot and `PushToggle` bell. About 48px plus the safe-area top. The Health page uses the same bar.
2. **Today strip.** One 44px row: `Entries 7 · Exits 7 · Open 0 · P&L +₹X`. "Total signals" and "Sound" are removed. A tap on "Open" switches to Positions.
3. **Segmented control.** `Log · Positions · Trades · More`, plus a filter icon button at the right end. It sticks under the app bar while the list scrolls.
4. **Filter chips.** Shown only when a filter is active, e.g. `NIFTY50_SR ✕`. With no filter they take no space.
5. **List.**

### Filter sheet

The filter button opens a bottom sheet with strategy, action and token fields, plus Clear and Apply. It uses the same sheet component as §6.

### Log cards

- A sticky day header: `Thu 08 Oct · 3 in · 3 out · +₹X`. Tap to collapse. The newest day starts expanded, as today.
- Each signal is one card, 56–64px tall:
  - Left: time, action badge, side badge.
  - Middle: instrument (`fno_symbol`, else `exchange:token`) and the strategy in muted text.
  - Right: price, and a colored P&L for EXIT and priced WATCH_EXIT rows.
  - A small dot shows the mode: Real, Paper, Capped or Shadow.
- A tap opens the **detail sheet**: action, side, mode, market state, strategy, instrument, price, entry pairing and P&L, stop loss, the full reason, and both timestamps.

### Position cards

Side, instrument, entry → current, live P&L in large type, stop loss and the exit-watch badge. A tap opens the detail sheet.

### Trade cards

Entry → exit with both times, P&L and points. The day header shows the net P&L and trade count.

### Other

- Only expanded days render, as today. Virtualization is not needed at the 500-signal cap.
- Phone cards and desktop tables read the same derived data: day grouping, `entryPriceMap` and `buildOpenOrders`.

## 8. Implementation notes

- Market and feed state logic moves out of `Header.tsx` into `hooks/useMarketFeedStatus.ts`. `Header`, `StatusDot` and `PageAppBar` use it.
- `App.tsx` renders `<Header/>` only when the phone-portrait query is false. On a phone each page renders its own bar; the dashboard uses the chart top bar.
- `hooks/useSheetDrag.ts` holds the pointer-drag and velocity logic now inside `SRSheet.tsx:107-150`. `SRSheet`, the Indicators sheet, the filter sheet and the detail sheet use it.
- Day grouping and entry pairing move out of `SignalsPage.tsx` into a `useSignalLog(filters)` hook.
- Cell formatters in `SignalRow.tsx` (side, mode, market, P&L) move into `signalFormat.ts`, so the table and the cards share them.
- New phone components live in `components/signals/mobile/`.
- `utils/haptic.ts` holds `haptic()`.
- Money stays in `int64` paise until display, as today.

### Review notes (2026-10-09)

These came from a review of the draft against the code.

- **One market-status fetch.** `Header.tsx:35` fetches `/api/market-status` every 5 minutes inside the component. If `Header`, `StatusDot` and `PageAppBar` each call the hook, the request runs two or three times. Move the market status into `useWSStore` (or a small store) with one poller started in `App.tsx`. `useMarketFeedStatus` only reads it.
- **Android back button.** In an installed app, the back gesture must close the top sheet first, then leave a "More" sub-view. Without this, back leaves the app or jumps to the previous tab. Each open sheet and sub-view pushes a `history.state` entry (no URL path change, so no new route). A `popstate` listener closes it. Closing by tap or swipe calls `history.back()` so the stack stays balanced.
- **"More" sub-views use a search param.** `/signals?view=fno|today|pnl`. Reload and back then work, and the route table does not change.
- **Today P&L has no store.** `pub:pnl` reaches components only through `window` `ws:message` events (`useWebSocket.ts:421`, listened to in `PnLSummaryTab.tsx:37` and `DailyAnalyticsTab.tsx`). The today strip needs the same: either a third listener, or a small `usePnLStore` fed once from `useWebSocket.ts`. Use the store; three ad-hoc listeners is the pattern to stop.
- **Pull to refresh is cut.** The browser's native pull-to-refresh is off in standalone mode, and a custom one adds gesture conflicts with the sheets. The list already updates live over WS. Drop it from this release.
- **Shell scroll.** The Signals and Health pages scroll the document (`.shell`, no `shellFixed`). Sticky segments and day headers need `top: <app bar height + safe-area top>`. Make the app bar sticky at `top: 0` so both stick in order.
- **Health tab components take no props.** `AccountTab`, `SessionHealthTab` and `TradingSettingsTab` render standalone, so the Health page can mount them as-is.
- **iOS has no vibration.** `haptic()` is Android-only. Press tint is the feedback that works everywhere.
- **Sound toggle.** Removed everywhere (2026-10-09, owner decision). `audioEnabled`/`toggleAudio` are gone from `useSignalStore`.

## 9. Acceptance checklist

Check at 360×740 and 390×844 (portrait), 844×390 (landscape) and 1440×900 (desktop).

Dashboard:

- [ ] One top bar. No brand bar, no TF pill row.
- [ ] The chart is about 100px taller than before.
- [ ] The status dot color matches the market and feed state. A tap shows the toast.
- [ ] A tap on the TF chip opens the OS picker. A new TF reloads the candles.
- [ ] The OHLC legend stays on one line.
- [ ] The SR chip does not cover candles or the `SIDEWAYS` label. A tap opens the SR sheet.
- [ ] The safe areas are correct on a notched phone, launched from the home screen.
- [ ] A 640×360 landscape phone keeps the landscape layout, not the portrait bar.
- [ ] Android back closes an open sheet first and never leaves the app from a sheet.

Indicators sheet:

- [ ] It slides up from the bottom. The keyboard does not open.
- [ ] Swipe down, a backdrop tap, ✕ and Escape all close it.
- [ ] Quick-add chips sit above the custom-add form.
- [ ] The footer stays visible while the keyboard is open.

Signals:

- [ ] No sideways scroll anywhere.
- [ ] The today strip, the segments and the first card are above the fold.
- [ ] A card tap opens the detail sheet with the full reason.
- [ ] The filter sheet sets chips. A chip ✕ clears that filter.
- [ ] Positions shows live P&L.
- [ ] More reaches FNO instruments, Today and P&L.
- [ ] Health shows Account, Session health and Settings.

Unchanged:

- [ ] Phone landscape looks the same as before.
- [ ] Desktop looks the same as before (compare screenshots).
