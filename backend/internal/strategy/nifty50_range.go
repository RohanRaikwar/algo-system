package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// Nifty50Range trades a range-bound NIFTY (S1 mean reversion + S2
// breakout, see nifty50_range_state.go). Signals trigger on 1m closes;
// option premium stops run on ticks.
type Nifty50Range struct {
	mu          sync.Mutex
	instruments map[string]*nifty50RangeState
	qty         int64
	cfg         Nifty50RangeConfig
	expiry      time.Time // weekly expiry traded, for days-to-expiry

	vol minuteVolume // futures volume per minute (cfg.VolumeToken)

	rejects map[string]int // first failing mean-reversion filter per entry bar
}

// minuteVolume turns cumulative day-volume ticks into per-minute volume.
type minuteVolume struct {
	minute      time.Time
	startVol    int64 // day volume when the minute began
	lastDayVol  int64
	initialized bool
}

// observe feeds one tick; it returns a finished minute and its volume.
func (m *minuteVolume) observe(ts time.Time, dayVol int64) (time.Time, int64, bool) {
	if dayVol <= 0 {
		return time.Time{}, 0, false
	}
	minute := ts.Truncate(time.Minute)
	if !m.initialized {
		m.minute, m.startVol, m.lastDayVol, m.initialized = minute, dayVol, dayVol, true
		return time.Time{}, 0, false
	}
	if dayVol < m.lastDayVol { // new session: counter reset
		m.minute, m.startVol, m.lastDayVol = minute, 0, dayVol
		return time.Time{}, 0, false
	}
	var done time.Time
	var vol int64
	var ok bool
	if minute.After(m.minute) {
		done, vol, ok = m.minute, m.lastDayVol-m.startVol, true
		m.minute, m.startVol = minute, m.lastDayVol
	}
	m.lastDayVol = dayVol
	return done, vol, ok
}

// SetVolumeToken names the instrument ("NFO:<token>") whose ticks supply
// volume, normally the current-month NIFTY future.
func (s *Nifty50Range) SetVolumeToken(token string) {
	s.mu.Lock()
	s.cfg.VolumeToken = token
	s.vol = minuteVolume{}
	s.mu.Unlock()
}

func NewNifty50Range(qty int64) *Nifty50Range {
	return NewNifty50RangeWithConfig(qty, DefaultNifty50RangeConfig())
}

func NewNifty50RangeWithConfig(qty int64, cfg Nifty50RangeConfig) *Nifty50Range {
	return &Nifty50Range{
		instruments: make(map[string]*nifty50RangeState, 4),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50Range) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_RANGE"
}

func (s *Nifty50Range) Config() Nifty50RangeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Nifty50Range) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

// SetFNOEntryPrice records the option fill so premium stops can run.
func (s *Nifty50Range) SetFNOEntryPrice(price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone {
			st.FNOEntryPrice = price
			st.FNOBestPrice = price
		}
	}
}

// SetExpiry sets the weekly expiry the options trade, for days-to-expiry.
func (s *Nifty50Range) SetExpiry(expiry time.Time) {
	s.mu.Lock()
	s.expiry = expiry
	s.mu.Unlock()
}

// otmSteps applies the near-expiry rule: within ATMWithinDTE days an OTM
// option's delta falls fast, so the strike goes back to ATM.
func (s *Nifty50Range) otmSteps(ts time.Time, steps int) int {
	if dte := daysToExpiry(ts, s.expiry); dte >= 0 && dte <= s.cfg.ATMWithinDTE {
		return 0
	}
	return steps
}

// SetPositionToken records the contract ("NFO:<token>") the open position
// was entered in, so premium stops follow that strike, not the ATM pair.
func (s *Nifty50Range) SetPositionToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone {
			st.FNOToken = token
		}
	}
}

// CancelEntry undoes an entry the engine could not place (no contract or
// no premium): the position is dropped and the trade is not counted.
func (s *Nifty50Range) CancelEntry(side PositionSide, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != side || side == SideNone {
			continue
		}
		log.Printf("[strategy] %s: %s entry cancelled — %s", s.Name(), side, reason)
		s.resetPosition(st)
		st.CooldownLeft = 0
		if st.TradesToday > 0 {
			st.TradesToday--
		}
	}
}

// CurrentFNOPosition reports the open option position, if any.
func (s *Nifty50Range) CurrentFNOPosition() *LiveFNOPosition {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side == SideNone || st.FNOEntryPrice == 0 {
			continue
		}
		token := st.FNOToken
		if token == "" {
			token = s.cfg.FNOPutToken
			if st.Side == SideCall {
				token = s.cfg.FNOCallToken
			}
		}
		if token == "" {
			return nil
		}
		return &LiveFNOPosition{Side: st.Side, Token: token, EntryPrice: st.FNOEntryPrice, BestPrice: st.FNOBestPrice}
	}
	return nil
}

// RangeState returns the current range view for the index (for logs/UI).
func (s *Nifty50Range) RangeState(key string) (RangeState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instruments[key]
	if !ok {
		return RangeState{}, false
	}
	return st.ctx.State(), true
}

// Warmup replays closed 1m history into the range context without
// emitting signals, so 15m ADX and levels are ready at the open.
func (s *Nifty50Range) Warmup(candles []model.TFCandle) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range candles {
		if c.TF != tf1m || c.Forming || (s.cfg.IndexToken != "" && c.Key() != s.cfg.IndexToken) {
			continue
		}
		st := s.getOrCreate(c.Key())
		if st.ctx.update(c.TS, barOf(c)) {
			n++
			if c.TS.After(st.LastCloseTS) {
				st.LastCloseTS = c.TS
			}
		}
	}
	return n
}

func barOf(c model.TFCandle) ohlcv {
	return ohlcv{Open: c.Open, High: c.High, Low: c.Low, Close: c.Close, Volume: c.Volume}
}

func (s *Nifty50Range) OnTFCandle(candle model.TFCandle) *Signal {
	if candle.TF != tf1m {
		return nil
	}
	if s.cfg.IndexToken != "" && candle.Key() != s.cfg.IndexToken {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getOrCreate(candle.Key())
	if !st.LastCloseTS.IsZero() && !candle.TS.After(st.LastCloseTS) {
		return nil
	}
	st.LastCloseTS = candle.TS

	st.ctx.update(candle.TS, barOf(candle))
	rs := st.ctx.State()
	// The breakout reference is the last range seen with price inside it:
	// once price is through a level, the level no longer brackets price.
	prevSup, prevRes, prevWidth := st.LastSup, st.LastRes, st.LastWidth
	if rs.InRange && candle.Close >= rs.Support && candle.Close <= rs.Resistance {
		st.LastSup, st.LastRes, st.LastWidth = rs.Support, rs.Resistance, rs.Width
	}

	day := candle.TS.In(ist).Format("2006-01-02")
	if st.TradeDay != day {
		st.TradeDay, st.TradesToday = day, 0
		st.PendingBreak = SideNone
		st.DayOpen = candle.Open
	}
	if st.CooldownLeft > 0 {
		st.CooldownLeft--
	}

	// TS is the bucket start; the bar closed one minute later.
	closeMin := minutesIST(candle.TS) + 1
	if closeMin <= sessionOpenMin || closeMin > hhmm(15, 30) {
		return nil
	}

	if st.Side != SideNone && closeMin >= s.cfg.TimeExitMin {
		return s.exit(candle, st, fmt.Sprintf("RANGE %s TIME EXIT %02d:%02d close=%d",
			st.Side, closeMin/60, closeMin%60, candle.Close))
	}
	if closeMin >= s.cfg.TimeExitMin {
		return nil
	}

	entryBar := s.entryBarClosed(st)
	if entryBar {
		if sig := s.evaluateBreakout(candle, st, prevSup, prevRes, prevWidth, closeMin); sig != nil {
			return sig
		}
		if !rs.InRange {
			s.evaluateFlag(candle, st, closeMin)
		}
	}
	if sig := s.evaluateExit(candle, st, entryBar); sig != nil {
		return sig
	}
	if !entryBar {
		return nil
	}
	return s.evaluateMeanReversion(candle, st, rs, closeMin)
}

// entryBarClosed reports whether this 1m close also closed an entry bar.
func (s *Nifty50Range) entryBarClosed(st *nifty50RangeState) bool {
	switch s.cfg.EntryTFMinutes {
	case 5:
		return st.ctx.closed5
	case 2, 3:
		return st.ctx.closedE
	}
	return true
}

// entryBars returns the previous and current entry-timeframe bars.
func (s *Nifty50Range) entryBars(st *nifty50RangeState) (prev, cur ohlcv, ok bool) {
	switch s.cfg.EntryTFMinutes {
	case 5:
		return st.ctx.lastBars5()
	case 2, 3:
		return st.ctx.lastBarsE()
	}
	return st.ctx.lastBars()
}

func (s *Nifty50Range) entryVolumeOK(st *nifty50RangeState, pct int64) bool {
	switch s.cfg.EntryTFMinutes {
	case 5:
		return st.ctx.volumeAtLeast5(pct, s.cfg.RequireVolume)
	case 2, 3:
		return st.ctx.volumeAtLeastE(pct, s.cfg.RequireVolume)
	}
	return st.ctx.volumeAtLeast(pct, s.cfg.RequireVolume)
}

// htfAllows applies the optional 1h trend filter to a break direction. With
// the filter on, an unready 1h EMA blocks the trade.
func (s *Nifty50Range) htfAllows(st *nifty50RangeState, dir PositionSide) bool {
	if !s.cfg.HTFTrendFilter {
		return true
	}
	t := st.ctx.State().HTFTrend
	return (dir == SideCall && t > 0) || (dir == SidePut && t < 0)
}

// stopBuffer is the distance beyond a level for the index stop: the fixed
// minimum or StopATRPct of the 15m ATR, whichever is wider, so the stop
// sits outside normal noise.
func (s *Nifty50Range) stopBuffer(rs RangeState) int64 {
	buf := s.cfg.StopBufferPts
	if a := rs.ATR15 * s.cfg.StopATRPct / 100; a > buf {
		buf = a
	}
	return buf
}

// canEnter applies the daily trade cap and post-exit cooldown.
func (s *Nifty50Range) canEnter(st *nifty50RangeState) bool {
	return st.TradesToday < s.cfg.MaxTradesPerDay && st.CooldownLeft == 0
}

// isBrokenRange reports whether rs is the range that already broke.
func (s *Nifty50Range) isBrokenRange(st *nifty50RangeState, sup, res int64) bool {
	tol := s.cfg.Range.LevelTolerancePts
	return st.BrokenRes > 0 && absInt64(sup-st.BrokenSup) <= tol && absInt64(res-st.BrokenRes) <= tol
}

// ── S2 breakout ──

func (s *Nifty50Range) evaluateBreakout(candle model.TFCandle, st *nifty50RangeState, sup, res, width int64, closeMin int) *Signal {
	if !s.cfg.BreakoutEnabled {
		return nil
	}
	confirm := s.cfg.BreakoutConfirmPts

	if st.PendingBreak != SideNone {
		dir, edge, move, kind := st.PendingBreak, st.PendingEdge, st.PendingWidth, st.PendingKind
		st.PendingBreak = SideNone
		held := (dir == SideCall && candle.Close >= edge+confirm) || (dir == SidePut && candle.Close <= edge-confirm)
		if !held {
			log.Printf("[strategy] %s: false breakout %s edge=%d close=%d — no follow-through", s.Name(), dir, edge, candle.Close)
			return nil
		}
		return s.enterBreakout(candle, st, dir, edge, move, kind)
	}

	if res <= sup || !inWindows(closeMin, s.cfg.BreakoutWindows) || s.isBrokenRange(st, sup, res) {
		return nil
	}
	_, cur, ok := s.entryBars(st)
	if !ok || !s.entryVolumeOK(st, s.cfg.BreakoutVolPct) {
		return nil
	}
	strong := cur.body()*2 >= cur.rng() // body at least half the bar
	switch {
	case strong && cur.bullish() && candle.Close >= res+confirm && s.htfAllows(st, SideCall):
		st.PendingBreak, st.PendingEdge = SideCall, res
	case strong && cur.bearish() && candle.Close <= sup-confirm && s.htfAllows(st, SidePut):
		st.PendingBreak, st.PendingEdge = SidePut, sup
	default:
		return nil
	}
	st.PendingWidth = width * s.cfg.BreakoutTargetPct / 100
	if st.PendingWidth <= 0 {
		st.PendingWidth = width
	}
	st.PendingKind = RangeKindBreakout
	st.BrokenSup, st.BrokenRes = sup, res
	log.Printf("[strategy] %s: breakout pending %s edge=%d close=%d — waiting for follow-through",
		s.Name(), st.PendingBreak, st.PendingEdge, candle.Close)
	return nil
}

func (s *Nifty50Range) enterBreakout(candle model.TFCandle, st *nifty50RangeState, dir PositionSide, edge, move int64, kind string) *Signal {
	if st.Side == dir {
		return nil // already positioned with the break
	}
	if st.TradesToday >= s.cfg.MaxTradesPerDay {
		return nil
	}
	reversing := st.Side != SideNone
	if !reversing && st.CooldownLeft > 0 {
		return nil
	}
	from := st.Side

	sign := int64(1)
	if dir == SidePut {
		sign = -1
	}
	if kind == "" {
		kind = RangeKindBreakout
	}
	stopBuf := s.cfg.BreakoutStopPts
	if b := s.stopBuffer(st.ctx.State()); b > stopBuf {
		stopBuf = b
	}
	if !s.rewardRiskOK(candle.Close, edge-sign*stopBuf, edge+sign*move) {
		log.Printf("[strategy] %s: breakout %s skipped — reward:risk below %d%%", s.Name(), dir, s.cfg.MinRewardRiskPct)
		return nil
	}
	s.openPosition(st, dir, kind, candle.Close,
		edge-sign*stopBuf, // stop back inside the range
		edge+sign*move,    // part of the measured move
		edge+sign*move/2)
	if kind == RangeKindBreakout {
		st.RangeSup, st.RangeRes = st.BrokenSup, st.BrokenRes
	}

	st.Strike = entryStrike(dir, candle.Close, s.cfg.StrikeStep, s.otmSteps(candle.TS, s.cfg.BreakoutOTMSteps))
	reason := fmt.Sprintf("RANGE %s %s edge=%d stop=%d target=%d strike=%d close=%d",
		kind, dir, edge, st.StopLevel, st.TargetLevel, st.Strike, candle.Close)
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	if reversing {
		return &Signal{
			StrategyName: s.Name(), Action: ActionExit, Side: from, ReverseTo: dir,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Price: candle.Close,
			MarketState: string(EntryMarketStateRange), Reason: reason,
			Strike:     st.Strike, // carried to the reverse BUY by expandReverseSignals
			TargetMove: absInt64(st.TargetLevel - candle.Close),
		}
	}
	return &Signal{
		StrategyName: s.Name(), Action: ActionBuy, Side: dir,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: string(EntryMarketStateRange), Reason: reason, Strike: st.Strike,
		TargetMove: absInt64(st.TargetLevel - candle.Close),
	}
}

// ── S2b flag (consolidation breakout on trend days) ──

// evaluateFlag arms a breakout from a tight 5m box that formed in the
// day's trend, when the break is in the trend direction. The next entry
// bar confirms it through the same pending-break path as S2.
func (s *Nifty50Range) evaluateFlag(candle model.TFCandle, st *nifty50RangeState, closeMin int) {
	if !s.cfg.FlagEnabled || st.Side != SideNone || st.PendingBreak != SideNone || st.DayOpen <= 0 {
		return
	}
	if !inWindows(closeMin, s.cfg.FlagWindows) || !s.canEnter(st) {
		return
	}
	box, ok := s.flagBox(st)
	if !ok || !box.Tight || !box.TrendDay {
		return
	}
	cur := box.cur
	hi, lo, trend := box.Hi, box.Lo, box.Trend
	if !s.entryVolumeOK(st, s.cfg.BreakoutVolPct) {
		return
	}
	confirm := s.cfg.BreakoutConfirmPts
	strong := cur.body()*2 >= cur.rng()
	mid := (hi + lo) / 2
	pole := absInt64(st.DayOpen - mid)
	switch {
	case trend < 0 && strong && cur.bearish() && candle.Close <= lo-confirm && s.htfAllows(st, SidePut):
		st.PendingBreak, st.PendingEdge = SidePut, lo
	case trend > 0 && strong && cur.bullish() && candle.Close >= hi+confirm && s.htfAllows(st, SideCall):
		st.PendingBreak, st.PendingEdge = SideCall, hi
	default:
		return
	}
	st.PendingKind = RangeKindFlag
	st.PendingWidth = pole * s.cfg.FlagTargetPolePct / 100
	log.Printf("[strategy] %s: flag pending %s box=%d-%d day_open=%d close=%d — waiting for follow-through",
		s.Name(), st.PendingBreak, lo, hi, st.DayOpen, candle.Close)
}

// flagBoxState is the current FLAG candidate: the last FlagBars closed 5m
// bars (excluding the newest) and whether they qualify.
type flagBoxState struct {
	Lo, Hi   int64
	Trend    int64 // latest close − day open
	Tight    bool  // box height within FlagMaxWidthBps of price
	TrendDay bool  // |Trend| ≥ FlagMinTrendPts
	cur      tfBar // newest closed 5m bar (the potential break bar)
}

func (s *Nifty50Range) flagBox(st *nifty50RangeState) (flagBoxState, bool) {
	// The box is FlagBars 5m bars long (1h at 12); on a 2m/3m entry series
	// the same span is FlagBars×5/N bars.
	bars := st.ctx.bars5
	n := s.cfg.FlagBars
	if m := s.cfg.EntryTFMinutes; m == 2 || m == 3 {
		bars = st.ctx.barsE
		n = s.cfg.FlagBars * 5 / m
	}
	if n <= 0 || len(bars) < n+1 || st.DayOpen <= 0 {
		return flagBoxState{}, false
	}
	cur := bars[len(bars)-1]
	box := bars[len(bars)-1-n : len(bars)-1]
	day := cur.TS.In(ist).Format("2006-01-02")
	hi, lo := box[0].High, box[0].Low
	for _, b := range box {
		if b.TS.In(ist).Format("2006-01-02") != day {
			return flagBoxState{}, false // the box must sit inside today's session
		}
		hi, lo = maxInt64(hi, b.High), minInt64(lo, b.Low)
	}
	trend := cur.Close - st.DayOpen
	return flagBoxState{
		Lo: lo, Hi: hi, Trend: trend, cur: cur,
		Tight:    (hi-lo)*10000 <= cur.Close*s.cfg.FlagMaxWidthBps,
		TrendDay: absInt64(trend) >= s.cfg.FlagMinTrendPts,
	}, true
}

// ── S1 mean reversion ──

func (s *Nifty50Range) evaluateMeanReversion(candle model.TFCandle, st *nifty50RangeState, rs RangeState, closeMin int) *Signal {
	if !s.cfg.MeanReversionEnabled || st.Side != SideNone || st.PendingBreak != SideNone {
		return nil
	}
	if !rs.InRange {
		s.reject(regimeReason(rs, s.cfg.Range))
		return nil
	}
	switch {
	case !rs.RSIReady:
		s.reject("rsi_not_ready")
		return nil
	case !inWindows(closeMin, s.cfg.MeanRevWindows):
		s.reject("time_window")
		return nil
	case !s.canEnter(st):
		s.reject("trade_cap_or_cooldown")
		return nil
	case s.isBrokenRange(st, rs.Support, rs.Resistance):
		s.reject("broken_range")
		return nil
	}
	prev, cur, ok := s.entryBars(st)
	if !ok {
		return nil
	}
	if !s.entryVolumeOK(st, s.cfg.ReversalVolPct) {
		s.reject("volume")
		return nil
	}

	stopBuf := s.stopBuffer(rs)
	zone := rs.Width * s.cfg.EntryZonePct / 100
	tol := s.cfg.Range.LevelTolerancePts
	frac := targetFraction(rs.Class)
	close := candle.Close

	atSupport := close >= rs.Support-tol && close <= rs.Support+zone
	atResistance := close <= rs.Resistance+tol && close >= rs.Resistance-zone
	switch {
	case atSupport:
		stop, target := rs.Support-stopBuf, rs.Support+rs.Width*frac/100
		switch {
		case !bullishReversal(prev, cur):
			s.reject("no_pattern")
		case rs.RSI5 >= s.cfg.RSIBuyMax:
			s.reject("rsi")
		case s.cfg.RequirePrevOpposite && !prev.bearish():
			s.reject("prev_bar")
		case !s.rewardRiskOK(close, stop, target):
			s.reject("reward_risk")
		default:
			s.openPosition(st, SideCall, RangeKindMeanReversion, close, stop, target, rs.Support+rs.Width/2)
			st.RangeSup, st.RangeRes = rs.Support, rs.Resistance
			return s.entrySignal(candle, st, rs)
		}
	case atResistance:
		stop, target := rs.Resistance+stopBuf, rs.Resistance-rs.Width*frac/100
		switch {
		case !bearishReversal(prev, cur):
			s.reject("no_pattern")
		case rs.RSI5 <= s.cfg.RSISellMin:
			s.reject("rsi")
		case s.cfg.RequirePrevOpposite && !prev.bullish():
			s.reject("prev_bar")
		case !s.rewardRiskOK(close, stop, target):
			s.reject("reward_risk")
		default:
			s.openPosition(st, SidePut, RangeKindMeanReversion, close, stop, target, rs.Resistance-rs.Width/2)
			st.RangeSup, st.RangeRes = rs.Support, rs.Resistance
			return s.entrySignal(candle, st, rs)
		}
	default:
		s.reject("not_at_edge")
	}
	return nil
}

// regimeReason names why the range regime is off, for rejection stats.
func regimeReason(rs RangeState, cfg RangeContextConfig) string {
	switch {
	case !rs.ADXReady:
		return "adx_not_ready"
	case rs.ADX >= cfg.MaxADX:
		return "adx_trending"
	case cfg.RequireFlatEMA && !rs.EMAFlat:
		return "ema_sloping"
	case rs.Width == 0:
		return "no_levels"
	case rs.Width < cfg.MinRangePts:
		return "range_too_narrow"
	default:
		return "range_too_wide"
	}
}

// reject counts why a mean-reversion candidate bar was not traded.
func (s *Nifty50Range) reject(reason string) {
	if s.rejects == nil {
		s.rejects = make(map[string]int)
	}
	s.rejects[reason]++
}

// RejectStats returns how often each mean-reversion filter was the first
// to refuse an entry bar (for backtests and tuning).
func (s *Nifty50Range) RejectStats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.rejects))
	for k, v := range s.rejects {
		out[k] = v
	}
	return out
}

// rewardRiskOK reports index reward (entry→target) ≥ MinRewardRiskPct% of
// index risk (entry→stop). Works for both sides via absolute distances.
func (s *Nifty50Range) rewardRiskOK(entry, stop, target int64) bool {
	if s.cfg.MinRewardRiskPct <= 0 {
		return true
	}
	risk, reward := absInt64(entry-stop), absInt64(target-entry)
	if risk <= 0 || reward <= 0 {
		return false
	}
	return reward*100 >= risk*s.cfg.MinRewardRiskPct
}

func (s *Nifty50Range) entrySignal(candle model.TFCandle, st *nifty50RangeState, rs RangeState) *Signal {
	st.Strike = entryStrike(st.Side, candle.Close, s.cfg.StrikeStep, s.otmSteps(candle.TS, s.cfg.OTMSteps[rs.Class]))
	reason := fmt.Sprintf("RANGE %s %s range=%d-%d (%s) adx=%.1f rsi5=%.1f stop=%d target=%d strike=%d close=%d",
		st.Kind, st.Side, rs.Support, rs.Resistance, rs.Class, rs.ADX, rs.RSI5, st.StopLevel, st.TargetLevel, st.Strike, candle.Close)
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	return &Signal{
		StrategyName: s.Name(), Action: ActionBuy, Side: st.Side,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: string(EntryMarketStateRange), Reason: reason, Strike: st.Strike,
		TargetMove: absInt64(st.TargetLevel - candle.Close),
	}
}

func (s *Nifty50Range) openPosition(st *nifty50RangeState, side PositionSide, kind string, entry, stop, target, trailArm int64) {
	st.Side, st.Kind = side, kind
	st.IndexEntry, st.IndexBest = entry, entry
	st.StopLevel, st.TargetLevel, st.TrailArm = stop, target, trailArm
	st.TrailArmed = false
	st.Strike, st.FNOToken = 0, ""
	st.FNOEntryPrice, st.FNOBestPrice = 0, 0
	st.TradesToday++
}

// ── Exits (1m close, index levels) ──

func (s *Nifty50Range) evaluateExit(candle model.TFCandle, st *nifty50RangeState, entryBar bool) *Signal {
	if st.Side == SideNone {
		return nil
	}
	close := candle.Close
	long := st.Side == SideCall
	sign := int64(1)
	if !long {
		sign = -1
	}

	if (close-st.IndexBest)*sign > 0 {
		st.IndexBest = close
	}
	if !st.TrailArmed && (close-st.TrailArm)*sign >= 0 {
		st.TrailArmed = true
	}

	switch {
	case (close-st.StopLevel)*sign <= 0:
		return s.exit(candle, st, fmt.Sprintf("RANGE %s STOP index=%d stop=%d close=%d", st.Side, close, st.StopLevel, close))
	case (close-st.TargetLevel)*sign >= 0:
		return s.exit(candle, st, fmt.Sprintf("RANGE %s TARGET index=%d target=%d close=%d", st.Side, close, st.TargetLevel, close))
	case st.TrailArmed && (close-(st.IndexEntry+(st.IndexBest-st.IndexEntry)/2))*sign <= 0:
		return s.exit(candle, st, fmt.Sprintf("RANGE %s TRAIL gave back half of move best=%d close=%d", st.Side, st.IndexBest, close))
	}

	// Reversal against the position near the opposite edge.
	if entryBar && st.Kind == RangeKindMeanReversion && st.RangeRes > st.RangeSup {
		prev, cur, ok := s.entryBars(st)
		zone := (st.RangeRes - st.RangeSup) * s.cfg.EntryZonePct / 100
		if ok && long && close >= st.RangeRes-zone && bearishReversal(prev, cur) {
			return s.exit(candle, st, fmt.Sprintf("RANGE CALL REVERSAL at resistance=%d close=%d", st.RangeRes, close))
		}
		if ok && !long && close <= st.RangeSup+zone && bullishReversal(prev, cur) {
			return s.exit(candle, st, fmt.Sprintf("RANGE PUT REVERSAL at support=%d close=%d", st.RangeSup, close))
		}
	}
	return nil
}

func (s *Nifty50Range) exit(candle model.TFCandle, st *nifty50RangeState, reason string) *Signal {
	side := st.Side
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	s.resetPosition(st)
	return &Signal{
		StrategyName: s.Name(), Action: ActionExit, Side: side,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Price: candle.Close,
		Reason: reason,
	}
}

// ── Premium stops (ticks) ──

func (s *Nifty50Range) OnTick(tick model.Tick) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	tickKey := tick.Exchange + ":" + tick.Token
	if s.cfg.VolumeToken != "" && tickKey == s.cfg.VolumeToken {
		if minute, vol, ok := s.vol.observe(tick.CanonicalTS(), tick.DayVolume); ok {
			for _, st := range s.instruments {
				st.ctx.addVolume(minute, vol)
			}
		}
		return nil
	}
	for key, st := range s.instruments {
		if st.Side == SideNone || st.FNOEntryPrice <= 0 {
			continue
		}
		held := st.FNOToken
		if held == "" {
			held = s.cfg.FNOPutToken
			if st.Side == SideCall {
				held = s.cfg.FNOCallToken
			}
		}
		if held != "" && tickKey == held {
			return s.checkPremium(key, tick, st)
		}
	}
	return nil
}

func (s *Nifty50Range) checkPremium(key string, tick model.Tick, st *nifty50RangeState) *Signal {
	entry, price := st.FNOEntryPrice, tick.Price
	if price <= 0 {
		return nil
	}
	if price > st.FNOBestPrice {
		st.FNOBestPrice = price
	}
	best := st.FNOBestPrice

	var reason string
	switch {
	case s.cfg.FNOTargetPct > 0 && (price-entry)*100 >= entry*s.cfg.FNOTargetPct:
		reason = fmt.Sprintf("RANGE %s FNO TARGET premium=%d entry=%d", st.Side, price, entry)
	case s.cfg.FNOTrailSLPct > 0 && (best-entry)*100 >= entry*s.cfg.FNOTrailStartPct &&
		(best-price)*100 >= best*s.cfg.FNOTrailSLPct:
		reason = fmt.Sprintf("RANGE %s FNO TRAIL SL premium=%d best=%d", st.Side, price, best)
	case s.cfg.FNOHardSLPct > 0 && (entry-price)*100 >= entry*s.cfg.FNOHardSLPct:
		reason = fmt.Sprintf("RANGE %s FNO HARD SL premium=%d entry=%d", st.Side, price, entry)
	default:
		return nil
	}
	side := st.Side
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	s.resetPosition(st)
	exch, token := splitKey(key)
	return &Signal{
		StrategyName: s.Name(), Action: ActionExit, Side: side,
		Token: token, Exchange: exch, Qty: s.qty, Reason: reason,
	}
}

// ── Helpers ──

func (s *Nifty50Range) getOrCreate(key string) *nifty50RangeState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50RangeState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50Range) resetPosition(st *nifty50RangeState) {
	st.Side, st.Kind = SideNone, ""
	st.IndexEntry, st.IndexBest = 0, 0
	st.StopLevel, st.TargetLevel, st.TrailArm = 0, 0, 0
	st.TrailArmed = false
	st.RangeSup, st.RangeRes = 0, 0
	st.Strike, st.FNOToken = 0, ""
	st.FNOEntryPrice, st.FNOBestPrice = 0, 0
	st.CooldownLeft = s.cfg.CooldownCandles
}

func (s *Nifty50Range) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		s.resetPosition(st)
		st.CooldownLeft = 0
		st.PendingBreak = SideNone
	}
}

func (s *Nifty50Range) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sigs []Signal
	for key, st := range s.instruments {
		if st.Side == SideNone {
			continue
		}
		side := st.Side
		exch, token := splitKey(key)
		s.resetPosition(st)
		sigs = append(sigs, Signal{
			StrategyName: s.Name(), Action: ActionExit, Side: side,
			Token: token, Exchange: exch, Qty: s.qty, Reason: reason,
		})
	}
	return sigs
}

func (s *Nifty50Range) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50RangeSnapshot(s.Name(), s.instruments)
}

func (s *Nifty50Range) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty50RangeSnapshot(data)
	if err != nil {
		return err
	}
	if snap.Version != nifty50RangeSnapshotVersion {
		log.Printf("[strategy] %s: dropping v%d snapshot (want v%d)", s.Name(), snap.Version, nifty50RangeSnapshotVersion)
		return nil
	}
	m := make(map[string]*nifty50RangeState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50Range(s.cfg, is)
	}
	s.instruments = m
	return nil
}

// splitKey splits "exchange:token".
func splitKey(key string) (exch, token string) {
	if i := indexOf(key, ":"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func indexOf(s, substr string) int {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
