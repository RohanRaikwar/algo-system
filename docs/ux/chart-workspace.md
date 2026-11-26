# Chart Workspace — UX design plan

## Context

The desktop dashboard grew in layers: header nav, a toolbar row, a tab row, the chart, a strategy strip and a status bar. Each layer takes vertical space from the chart. The user shared a TradingView screenshot as the reference *layout* (not look): a thin top toolbar, a left tool rail, a full-bleed chart, a right icon rail that opens panels, and a bottom range bar.

Recent steps already moved this way: the reconnect banner is gone, there is a single chart, and S/R + F&O moved into a right sheet. This plan finishes the job. It gives one coherent workspace and writes the design down as `docs/ux/chart-workspace.md`.

Scope: desktop and tablet (> 640px). Phone keeps its current layout (own chart bar, SRPeek/SRSheet, MobileNav). **The theme does not change**: same colors, fonts, sizes and component styles. Only the layout changes.

Deliverable on approval:
1. Update `docs/ux/chart-workspace.md` in the repo to match this plan.
2. Build it in the phases below, one reviewable step at a time.

---

## 1. Subject, audience, job

- **Subject**: an intraday index-options desk for NIFTY, SENSEX and BANKNIFTY. Telegram calls drive the trades today, and S/R levels are the product's name ("Levels").
- **Audience**: one trader. They hold positions 1–5 h, watch the chart all day, and act on calls and stop lines.
- **Primary job**: see price against levels and open positions, and reach every trading panel without leaving the chart.

The layout must protect chart area first. Every pixel of chrome must earn its place.

## 2. Theme: unchanged

This is a layout change only. The theme stays exactly as it is:
- Colors: the existing `--sg-*` tokens in `src/styles/global.css` and the chart colors in `src/styles/palette.ts`. No new or edited tokens.
- Fonts and text sizes: Inter, JetBrains Mono, 14px base, as today.
- Radii, shadows, borders: `rounded-panel`, `rounded-ctrl`, `shadow-panel`, `shadow-ring`, as today.
- Components: the existing `src/components/ui/*` variants (Button, ToggleGroup, Tabs, Badge, NativeSelect, Kbd, Drawer) are reused as they are. No new variants and no restyling.
- Chart look: candles, price lines, SR lines, markers and legend keep their current styles.

New pieces (rails, dock panel, bottom bar) only arrange existing components. They use the same token classes the current Header, toolbar and StatusBar already use (`bg-sg-surface`, `border-sg-line`, `text-sg-muted`, …).

### Sizes (layout only)
- Top toolbar: same height as today's Header (52px). Bottom bar: same height as today's StatusBar (30px).
- Rails: 44px wide, holding the existing `Button` `size="icon-32"`.
- Right panel: 360px default, resizable 320–560px, width remembered. Resize uses shadcn `Resizable` (`src/components/ui/resizable.tsx`, `react-resizable-panels`): `ResizablePanelGroup` (chart panel + dock panel) with a `ResizableHandle` between them. The handle uses `bg-border` / `ring`, which map to `--sg-line` / `--sg-accent`, so no theme change.

## 3. Layout

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ ⚡ │ NIFTY 50 ▾  25,349.45 −579.70 (−2.24%) │ 1m 5m 15m 1h ▾ │ ƒ Indicators │ … │ ● Live  Market closed │ ⚙ │  ← top toolbar 52px
├────┬────────────────────────────────────────────────────────────────┬────┬───┤
│ ✛  │ O 25,513 H 25,515 L 25,507 C 25,508      (legend, top-left)     │    │ ◎ │ ← right rail
│ ╱  │ SMA 21  25,726                                                  │    │ ▤ │   S/R strategy
│ ═  │                                                                 │ P  │ ≡ │   F&O instruments
│ T  │                       chart (full bleed)                        │ a  │ ⇅ │   Signals
│ ⌖  │                                                                 │ n  │ 🔔│   Positions & orders
│────│                                                                 │ e  │   │   Alerts
│ 🧲 │                                                                 │ l  │   │
│ 👁 │                                                                 │    │   │
│ 🗑 │                                                    price axis │ │    │ ♥ │ ← Health (bottom of rail)
├────┴────────────────────────────────────────────────────────────────┴────┴───┤
│ 1D 5D 1M 3M │ ⟲ │                         01:20:58 IST · Last tick 3s · 42ms │ ← bottom bar 30px
└──────────────────────────────────────────────────────────────────────────────┘
       left rail 44px                                    panel 360px  rail 44px
```

- **Top toolbar** merges today's Header and chart toolbar into one row:
  - Left: brand mark only (icon, no wordmark), symbol picker, price and change.
  - Middle: timeframe dropdown (`TimeframeSelect` in `TradingChart.tsx`, a box with the current TF over a native `<select>`, shared with the phone bar), then Indicators.
  - Right: feed pill, market pill, a settings menu holding Keep awake, Push alerts and the page links (Signals, Telegram, Options, Health).
  - Left-aligned groups separated by 1px rules. No centered content.
- **Left rail**: the chart tools. Phase 1 ships only the tools that exist today: crosshair, S/R lines toggle, SR box toggle, trade-markers toggle, position-lines drag lock, reset view. Drawing tools (trend line, horizontal line, text) are phase 4.
- **Chart**: fills everything between the rails. Legend top-left. `SRSituationCard` and `OrderLineOverlay` stay where they are.
- **Right rail plus panel**: one icon per panel. Click opens the panel *docked* next to the rail, which shrinks the chart, unlike today's overlay drawer. Click again or Esc closes it. Only one panel is open at a time. The rail lists S/R strategy, F&O instruments, Signals (log), Positions & orders and Alerts, with Health pinned at the bottom. Telegram is not a panel: it stays its own screen (see below).
- **Bottom bar**: quick ranges (1D 5D 1M 3M set the visible range), reset to live, then on the right the IST clock, last tick age and latency. It replaces the `StatusBar` content.
- **Alignment**: everything left-aligned inside its zone. Numbers right-aligned in tables.
- **Telegram screen**: stays a separate full page at `/telegram`, not a sheet or dock panel. It keeps today's `TelegramPage` layout (status card, call-reader card, verdict counts, "Only trade messages" switch, message list), with the normal Header. It is reached from the top toolbar menu.

### Breakpoints
- **≥ 1100px**: as drawn.
- **641–1099px**: the left rail collapses to a 32px icon strip of essentials. The right panel floats over the chart instead of docking.
- **≤ 640px and phone-landscape**: unchanged current phone layout.

## 4. Principles

1. **Chart first.** No chrome row exists unless it holds an action used daily. Target: chart area ≥ 85% of the viewport at 1920×1080, against about 70% today.
2. **Same look, new arrangement.** Nothing changes color, font or style. A user should recognise every control; only where it sits changes.
3. **One panel, docked.** Panels push the chart rather than cover it, so price stays visible while reading signals or calls.
4. **Keyboard reachable.** `/` opens indicators, `1`–`5` toggle right-rail panels, `Esc` closes, `Alt+R` resets the view. Every rail icon has a tooltip naming its action and shortcut.
5. **Plain words.** Panel titles name what the user sees ("Positions & orders", not "LiveOrdersTab"). Empty states say what to do: "No open positions. Calls from Telegram appear here when they trigger."

## 5. Implementation phases

Reuse first; there are no new data paths.

### Phase 1 — shell
- New `src/components/workspace/` holding `WorkspaceLayout.tsx` (CSS grid: toolbar / left rail / chart / panel / right rail / bottom bar), `TopToolbar.tsx`, `ToolRail.tsx`, `PanelRail.tsx`, `DockPanel.tsx` and `BottomBar.tsx`.
- `src/App.tsx`: on `/` at desktop, render `WorkspaceLayout` with no `Header` and no `StatusBar`. Other routes keep the `Header`.
- Move the toolbar parts out of `src/components/chart/TradingChart.tsx` (symbol `NativeSelect`, price and change at 107–122, TF `ToggleGroup`, Indicators button) into `TopToolbar`. TradingChart keeps the phone bar, and gets a `chrome="none"` mode for the workspace.
- Reuse the feed and market pills from `Header.tsx` and `useMarketFeedStatus`. Reuse `KeepAwakeToggle` and `PushToggle` inside the settings menu.
- `BottomBar` reuses the `StatusBar.tsx` data (`useWSStore`).
- Remove the side `Drawer` from `src/pages/DashboardPage.tsx`; the dock replaces it.

### Phase 2 — right rail panels
- `DockPanel` hosts existing components: `SRPanel variant="sheet"`, `FnoInstrumentsTab`, the signal log (`SignalRow` + `useSignalsData`), `LiveOrdersTab`, `SystemHealth`. Not `TelegramPanel`: Telegram stays a separate screen.
- Alerts panel: a list of push alert rules plus the `AlertSetupDialog` entry. No in-app alert list exists, so v1 shows the setup only.
- Remember the open panel and its width in localStorage, wrapping every read and write in try/catch as the existing `loadSideTab` does.
- Keyboard: extend the handler in `src/App.tsx:72-86` with `1`–`5` and `Esc`.

### Phase 3 — tool rail and range bar
- Tool rail toggles bind to the existing overlay state in `src/store/useAppStore.ts` (`overlays.*`) and to the hooks `useSRLines`, `useSRBox`, `useChartMarkers` and `useTradeLineDrag`.
- Range buttons call `chart.timeScale().setVisibleRange()`, using `useChartLazyLoad` to fetch older bars when needed. The reset button reuses the double-click reset in `useChartInteraction`.

### Phase 4 — drawing tools (optional, separate decision)
- Trend line, horizontal line and text as lightweight-charts primitives, following `srBoxPrimitive.ts`. Stored per symbol in localStorage. This is the largest piece and can be skipped.

### Cleanup
- Delete the unused `compact`, `paneTF` and `onPaneTFChange` props in `TradingChart.tsx`, now that the multi-chart layout is gone.
- Fix `MobileNav` `repeat(4,1fr)` to 5 columns while in the area. This is a pre-existing bug.

## 6. Verification

- `cd frontend && npx tsc -b && npm test && npm run build`.
- Add unit tests for `WorkspaceLayout` panel state (open, close, switch, persisted width with storage blocked) and for the keyboard shortcuts, next to the existing tests in `src/**/__tests__`.
- Run `./scripts/air_local.sh` and check http://localhost:5173 with screenshots at 1920×1080, 1280×800, 1024×768, 900×500 (phone-land) and 390×844 (phone). Phone must look unchanged.
- Measure chart area at 1920×1080 against the ≥ 85% target.
- Theme check: `git diff --stat` shows no change to `src/styles/global.css`, `src/styles/palette.ts` or `src/components/ui/*`. Side-by-side screenshots: controls look the same as before, only placed differently.
- Check keyboard: Tab order through toolbar → rails → panel, a visible focus ring, and the `prefers-reduced-motion` panel slide.

## Option chain from the index bar

- The chart toolbar has an **Option chain** button next to the index picker (desktop and tablet only). It opens the selected index's chain (NIFTY 50 → NIFTY) in a modal `Dialog`. The chain follows the toolbar's index picker; it has no picker of its own here (`OptionChainPanel variant="panel"`).
- Clicking a CE or PE price closes the dialog and splits the chart area: index chart on the left, that contract's chart (`OptionChart fill`) on the right. The split starts at 50/50 and is resizable with the shadcn `ResizableHandle` (each side at least 25%). The dragged width is remembered in this browser (`useDefaultLayout`, key `dashboard-option-split`, throw-safe storage).
- ✕ on the option chart closes it and the index chart takes the full width again. Picking another contract replaces it. Changing the index closes it.
- The `/options` page stays a separate screen with its own dialog, unchanged.
