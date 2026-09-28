package strategy

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// Nifty50SR trades NIFTY options off support/resistance with the setup
// chosen by market regime (see nifty50_sr_state.go). Entries are judged
// on EntryTFMinutes closes (1m by default), index stops/targets on 1m
// closes, premium stops on ticks.
type Nifty50SR struct {
	mu          sync.Mutex
	instruments map[string]*nifty50SRState
	qty         int64
	cfg         Nifty50SRConfig
	expiry      time.Time

	vol minuteVolume

	rejects map[string]int
}

func NewNifty50SR(qty int64, cfg Nifty50SRConfig) *Nifty50SR {
	return &Nifty50SR{
		instruments: make(map[string]*nifty50SRState, 2),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50SR) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_SR"
}

func (s *Nifty50SR) Config() Nifty50SRConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SetVolumeToken names the instrument whose ticks supply volume (the
// NIFTY future); the index itself has none.
func (s *Nifty50SR) SetVolumeToken(token string) {
	s.mu.Lock()
	s.cfg.VolumeToken = token
	s.vol = minuteVolume{}
	s.mu.Unlock()
}

func (s *Nifty50SR) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	s.cfg.FNOCallToken, s.cfg.FNOPutToken = callToken, putToken
	s.mu.Unlock()
}

// SetExpiry sets the weekly expiry traded (for logs; strikes are picked
// by stratengine from the live chain).
func (s *Nifty50SR) SetExpiry(expiry time.Time) {
	s.mu.Lock()
	s.expiry = expiry
	s.mu.Unlock()
}

// SetFNOEntryPrice records the option fill so premium stops can run.
func (s *Nifty50SR) SetFNOEntryPrice(price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone {
			st.FNOEntryPrice, st.FNOBestPrice = price, price
		}
	}
}

// SetPositionToken records the contract the open position was bought in.
func (s *Nifty50SR) SetPositionToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone {
			st.FNOToken = token
		}
	}
}

// PremiumExitsOnTicks: premium stops run on option ticks.
func (s *Nifty50SR) PremiumExitsOnTicks() bool { return true }

// CancelEntry undoes an entry the engine could not place; it is not
// counted against the day's caps.
func (s *Nifty50SR) CancelEntry(side PositionSide, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if side == SideNone || st.Side != side {
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
func (s *Nifty50SR) CurrentFNOPosition() *LiveFNOPosition {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side == SideNone || st.FNOEntryPrice == 0 {
			continue
		}
		token := s.heldToken(st)
		if token == "" {
			return nil
		}
		return &LiveFNOPosition{Side: st.Side, Token: token, EntryPrice: st.FNOEntryPrice, BestPrice: st.FNOBestPrice}
	}
	return nil
}

// SRState returns the current view for the index (for logs/UI).
func (s *Nifty50SR) SRState(key string) (SRState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instruments[key]
	if !ok {
		return SRState{}, false
	}
	return st.ctx.State(), true
}

// Warmup replays closed 1m history without emitting signals.
func (s *Nifty50SR) Warmup(candles []model.TFCandle) int {
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

func (s *Nifty50SR) OnTFCandle(candle model.TFCandle) *Signal {
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
	st.LastClose = candle.Close

	day := candle.TS.In(ist).Format("2006-01-02")
	if st.TradeDay != day {
		st.TradeDay, st.TradesToday, st.ConsecLosses, st.DayPnLPts = day, 0, 0, 0
		st.PendingSide = SideNone
	}
	if st.CooldownLeft > 0 {
		st.CooldownLeft--
	}

	closeMin := minutesIST(candle.TS) + 1 // TS is the bucket start
	if closeMin <= sessionOpenMin || closeMin > hhmm(15, 30) {
		return nil
	}
	if closeMin >= s.cfg.TimeExitMin {
		if st.Side != SideNone {
			return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s TIME EXIT %02d:%02d close=%d",
				st.Side, closeMin/60, closeMin%60, candle.Close))
		}
		return nil
	}
	if sig := s.evaluateExit(candle, st); sig != nil {
		return sig
	}
	if !s.entryBarClosed(st) {
		return nil
	}
	if st.Side != SideNone {
		st.PendingSide = SideNone
		return nil
	}

	ss := st.ctx.State()
	prev, cur, ok := s.entryBars(st)
	if !ok || ss.Regime == SRRegimeNone {
		s.reject("not_ready")
		return nil
	}
	if ss.Regime == SRRegimeDead {
		st.PendingSide = SideNone
		s.reject("regime_dead")
		return nil
	}

	if st.PendingSide != SideNone {
		if sig := s.evaluateRetest(candle, st, ss, prev, cur, closeMin); sig != nil {
			return sig
		}
	}
	if st.PendingSide == SideNone && s.armBreak(st, ss, prev, cur) {
		return nil
	}
	switch ss.Regime {
	case SRRegimeRange:
		return s.evaluateFade(candle, st, ss, prev, cur, closeMin)
	case SRRegimeTrendUp, SRRegimeTrendDown:
		return s.evaluatePullback(candle, st, ss, prev, cur, closeMin)
	}
	s.reject("regime_mixed")
	return nil
}

func (s *Nifty50SR) entryTF() int {
	if s.cfg.EntryTFMinutes == 5 {
		return 5
	}
	return 1
}

// entryBarClosed reports whether this 1m close also closed an entry bar.
func (s *Nifty50SR) entryBarClosed(st *nifty50SRState) bool {
	return s.entryTF() == 1 || st.ctx.rc.closed5
}

// entryBars returns the previous and just-closed entry bars.
func (s *Nifty50SR) entryBars(st *nifty50SRState) (prev, cur ohlcv, ok bool) {
	if s.entryTF() == 5 {
		return st.ctx.rc.lastBars5()
	}
	return st.ctx.rc.lastBars()
}

func (s *Nifty50SR) entryVolumeOK(st *nifty50SRState, pct int64) bool {
	if s.entryTF() == 5 {
		return st.ctx.rc.volumeAtLeast5(pct, false)
	}
	return st.ctx.rc.volumeAtLeast(pct, false)
}

// retestBars is RetestMinutes in entry bars.
func (s *Nifty50SR) retestBars() int {
	n := s.cfg.RetestMinutes / s.entryTF()
	if n < 1 {
		n = 1
	}
	return n
}

// entryBlock names the day/time cap that refuses a new entry, "" if none.
func (s *Nifty50SR) entryBlock(st *nifty50SRState, closeMin int) string {
	switch {
	case closeMin < s.cfg.EntryFromMin || closeMin >= s.cfg.EntryToMin:
		return "time_window"
	case st.TradesToday >= s.cfg.MaxTradesPerDay:
		return "trade_cap"
	case s.cfg.MaxConsecLosses > 0 && st.ConsecLosses >= s.cfg.MaxConsecLosses:
		return "consec_losses"
	case s.cfg.MaxDayLossPts > 0 && st.DayPnLPts <= -s.cfg.MaxDayLossPts:
		return "day_loss"
	case st.CooldownLeft > 0:
		return "cooldown"
	}
	return ""
}

// stopBuffer is the distance beyond a level for the index stop.
func (s *Nifty50SR) stopBuffer(ss SRState) int64 {
	buf := ss.Range.ATR15 * s.cfg.StopATRPct / 100
	if buf < s.cfg.StopMinPts {
		buf = s.cfg.StopMinPts
	}
	return buf
}

// confirmations counts the indicator checks that agree with an entry.
type confirmations struct {
	VWAP, EMA, RSI, Candle bool
}

func (c confirmations) count() int {
	n := 0
	for _, ok := range [...]bool{c.VWAP, c.EMA, c.RSI, c.Candle} {
		if ok {
			n++
		}
	}
	return n
}

func (c confirmations) String() string {
	mark := func(b bool) string {
		if b {
			return "Y"
		}
		return "n"
	}
	return fmt.Sprintf("vwap=%s ema=%s rsi=%s candle=%s", mark(c.VWAP), mark(c.EMA), mark(c.RSI), mark(c.Candle))
}

// levelNear returns the level closest to price within tol, 0 if none.
func levelNear(levels []SRLevel, price, tol int64) int64 {
	var best, bestD int64
	for _, l := range levels {
		d := absInt64(l.Price - price)
		if d <= tol && (best == 0 || d < bestD) {
			best, bestD = l.Price, d
		}
	}
	return best
}

// ── FADE (RANGE regime) ──

func (s *Nifty50SR) evaluateFade(candle model.TFCandle, st *nifty50SRState, ss SRState, prev, cur ohlcv, closeMin int) *Signal {
	if !s.cfg.FadeEnabled {
		return nil
	}
	tol, close := s.cfg.TouchTolPts, cur.Close
	flat := absInt64(ss.EMAFast-ss.EMASlow) <= s.cfg.FlatEMAPts
	buf := s.stopBuffer(ss)

	if l := levelNear(ss.Levels, cur.Low, tol); l > 0 && l < close {
		c := confirmations{
			VWAP:   close < ss.VWAP, // room to revert up to VWAP
			EMA:    flat || ss.EMAFast > ss.EMASlow,
			RSI:    ss.Range.RSIReady && ss.Range.RSI5 < s.cfg.FadeRSIBuyMax,
			Candle: bullishReversal(prev, cur),
		}
		if !c.Candle {
			s.reject("fade:no_pattern")
			return nil
		}
		return s.tryEnter(candle, st, ss, SideCall, SRKindFade, l, l-buf, levelAbove(ss.Levels, close+tol), c, closeMin)
	}
	if l := levelNear(ss.Levels, cur.High, tol); l > 0 && l > close {
		c := confirmations{
			VWAP:   close > ss.VWAP,
			EMA:    flat || ss.EMAFast < ss.EMASlow,
			RSI:    ss.Range.RSIReady && ss.Range.RSI5 > s.cfg.FadeRSISellMin,
			Candle: bearishReversal(prev, cur),
		}
		if !c.Candle {
			s.reject("fade:no_pattern")
			return nil
		}
		return s.tryEnter(candle, st, ss, SidePut, SRKindFade, l, l+buf, levelBelow(ss.Levels, close-tol), c, closeMin)
	}
	s.reject("fade:not_at_level")
	return nil
}

// ── BREAKOUT-RETEST (any regime but DEAD) ──

// armBreak arms a retest after a strong entry-bar close through a level on the
// VWAP side of the break. It reports whether a break was armed.
func (s *Nifty50SR) armBreak(st *nifty50SRState, ss SRState, prev, cur ohlcv) bool {
	if !s.cfg.RetestEnabled || cur.rng() <= 0 || cur.body()*2 < cur.rng() {
		return false
	}
	if !s.entryVolumeOK(st, s.cfg.BreakVolPct) {
		return false
	}
	beyond := ss.Range.ATR15 * s.cfg.BreakATRPct / 100
	var side PositionSide
	var level int64
	for _, l := range ss.Levels {
		switch {
		case cur.bullish() && cur.Close > ss.VWAP && prev.Close <= l.Price && cur.Close >= l.Price+beyond:
			if side != SideCall || l.Price > level { // highest level broken
				side, level = SideCall, l.Price
			}
		case cur.bearish() && cur.Close < ss.VWAP && prev.Close >= l.Price && cur.Close <= l.Price-beyond:
			if side != SidePut || l.Price < level { // lowest level broken
				side, level = SidePut, l.Price
			}
		}
	}
	if side == SideNone || level == 0 {
		return false
	}
	st.PendingSide, st.PendingLevel, st.PendingLeft = side, level, s.retestBars()
	log.Printf("[strategy] %s: break %s level=%d close=%d — waiting %d min for retest",
		s.Name(), side, level, cur.Close, s.cfg.RetestMinutes)
	return true
}

func (s *Nifty50SR) evaluateRetest(candle model.TFCandle, st *nifty50SRState, ss SRState, prev, cur ohlcv, closeMin int) *Signal {
	side, l, tol := st.PendingSide, st.PendingLevel, s.cfg.TouchTolPts
	st.PendingLeft--
	expire := func(reason string) {
		st.PendingSide = SideNone
		s.reject("retest:" + reason)
	}
	long := side == SideCall
	if (long && cur.Close < l-tol) || (!long && cur.Close > l+tol) {
		log.Printf("[strategy] %s: break %s level=%d failed close=%d", s.Name(), side, l, cur.Close)
		expire("failed")
		return nil
	}
	var touched, rejected bool
	var c confirmations
	if long {
		touched = cur.Low <= l+tol && cur.Close > l
		rejected = bullishReversal(prev, cur) || (cur.bullish() && cur.lowerWick() >= cur.body())
		c = confirmations{
			VWAP: cur.Close > ss.VWAP, EMA: ss.EMAFast > ss.EMASlow,
			RSI: ss.Range.RSIReady && ss.Range.RSI5 > 50, Candle: rejected,
		}
	} else {
		touched = cur.High >= l-tol && cur.Close < l
		rejected = bearishReversal(prev, cur) || (cur.bearish() && cur.upperWick() >= cur.body())
		c = confirmations{
			VWAP: cur.Close < ss.VWAP, EMA: ss.EMAFast < ss.EMASlow,
			RSI: ss.Range.RSIReady && ss.Range.RSI5 < 50, Candle: rejected,
		}
	}
	if touched && rejected {
		buf := s.stopBuffer(ss)
		stop, target := l-buf, levelAbove(ss.Levels, cur.Close+tol)
		if !long {
			stop, target = l+buf, levelBelow(ss.Levels, cur.Close-tol)
		}
		if sig := s.tryEnter(candle, st, ss, side, SRKindRetest, l, stop, target, c, closeMin); sig != nil {
			st.PendingSide = SideNone
			return sig
		}
	}
	if st.PendingLeft <= 0 {
		expire("expired")
	}
	return nil
}

// ── PULLBACK (TREND regimes) ──

func (s *Nifty50SR) evaluatePullback(candle model.TFCandle, st *nifty50SRState, ss SRState, prev, cur ohlcv, closeMin int) *Signal {
	if !s.cfg.PullbackEnabled {
		return nil
	}
	tol, close := s.cfg.TouchTolPts, cur.Close
	long := ss.Regime == SRRegimeTrendUp
	side := SideCall
	anchors := []int64{ss.VWAP, ss.EMASlow, levelBelow(ss.Levels, close)}
	if !long {
		side = SidePut
		anchors[2] = levelAbove(ss.Levels, close)
	}
	var anchor int64
	for _, a := range anchors {
		if a <= 0 {
			continue
		}
		if (long && cur.Low <= a+tol && close > a) || (!long && cur.High >= a-tol && close < a) {
			anchor = a
			break
		}
	}
	if anchor == 0 {
		s.reject("pullback:no_touch")
		return nil
	}
	var c confirmations
	var stop int64
	buf := s.stopBuffer(ss)
	rsi := ss.Range.RSI5
	if long {
		if !cur.bullish() {
			s.reject("pullback:no_close_back")
			return nil
		}
		c = confirmations{
			VWAP: close > ss.VWAP, EMA: ss.EMAFast > ss.EMASlow,
			RSI:    ss.Range.RSIReady && rsi >= s.cfg.PullRSILo && rsi <= s.cfg.PullRSIHi+5,
			Candle: bullishReversal(prev, cur) || cur.lowerWick() >= cur.body() || close > prev.High,
		}
		stop = minInt64(anchor, cur.Low) - buf
	} else {
		if !cur.bearish() {
			s.reject("pullback:no_close_back")
			return nil
		}
		// Mirror of the uptrend band around 50.
		c = confirmations{
			VWAP: close < ss.VWAP, EMA: ss.EMAFast < ss.EMASlow,
			RSI:    ss.Range.RSIReady && rsi <= 100-s.cfg.PullRSILo && rsi >= 100-s.cfg.PullRSIHi-5,
			Candle: bearishReversal(prev, cur) || cur.upperWick() >= cur.body() || close < prev.Low,
		}
		stop = maxInt64(anchor, cur.High) + buf
	}
	// Trend target is DefaultTargetR; a level closer than MinRewardRiskPct
	// is resistance in the way (support for a PUT) and refuses the entry.
	risk := absInt64(close - stop)
	target := close + risk*s.cfg.DefaultTargetR/100
	next := levelAbove(ss.Levels, close)
	if !long {
		target = close - risk*s.cfg.DefaultTargetR/100
		next = levelBelow(ss.Levels, close)
	}
	if next > 0 && absInt64(next-close)*100 < risk*s.cfg.MinRewardRiskPct {
		s.reject("pullback:level_in_way")
		return nil
	}
	return s.tryEnter(candle, st, ss, side, SRKindPullback, anchor, stop, target, c, closeMin)
}

// tryEnter applies confirmations, caps and reward:risk, then opens the
// position. target 0 means "no next level": DefaultTargetR is used.
func (s *Nifty50SR) tryEnter(candle model.TFCandle, st *nifty50SRState, ss SRState, side PositionSide, kind string,
	level, stop, target int64, c confirmations, closeMin int) *Signal {
	if n := c.count(); n < s.cfg.MinConfirmations {
		s.reject(fmt.Sprintf("%s:confirmations_%d", kindTag(kind), n))
		return nil
	}
	if why := s.entryBlock(st, closeMin); why != "" {
		s.reject(why)
		return nil
	}
	entry := candle.Close
	risk := absInt64(entry - stop)
	if risk <= 0 || (side == SideCall && stop >= entry) || (side == SidePut && stop <= entry) {
		s.reject(kindTag(kind) + ":bad_stop")
		return nil
	}
	if target == 0 {
		if side == SideCall {
			target = entry + risk*s.cfg.DefaultTargetR/100
		} else {
			target = entry - risk*s.cfg.DefaultTargetR/100
		}
	}
	reward := target - entry
	if side == SidePut {
		reward = entry - target
	}
	if reward <= 0 || reward*100 < risk*s.cfg.MinRewardRiskPct {
		s.reject(kindTag(kind) + ":reward_risk")
		return nil
	}

	st.Side, st.Kind, st.Regime = side, kind, ss.Regime
	st.IndexEntry, st.StopLevel, st.TargetLevel, st.Risk = entry, stop, target, risk
	st.Breakeven = false
	st.FNOToken, st.FNOEntryPrice, st.FNOBestPrice = "", 0, 0
	st.Strike = atmStrike(entry, s.cfg.StrikeStep)
	st.TradesToday++

	reason := fmt.Sprintf("SR %s %s regime=%s level=%d stop=%d target=%d vwap=%d ema=%d/%d rsi5=%.1f adx=%.1f %s strike=%d close=%d",
		kind, side, ss.Regime, level, stop, target, ss.VWAP, ss.EMAFast, ss.EMASlow, ss.Range.RSI5, ss.Range.ADX, c, st.Strike, entry)
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	return &Signal{
		StrategyName: s.Name(), Action: ActionBuy, Side: side,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: srMarketState(ss.Regime), Reason: reason, Strike: st.Strike,
		TargetMove: reward,
	}
}

func kindTag(kind string) string {
	switch kind {
	case SRKindFade:
		return "fade"
	case SRKindRetest:
		return "retest"
	case SRKindPullback:
		return "pullback"
	}
	return kind
}

// srMarketState maps the regime to the signal's market_state label.
func srMarketState(r SRRegime) string {
	switch r {
	case SRRegimeRange:
		return string(EntryMarketStateRange)
	case SRRegimeTrendUp, SRRegimeTrendDown:
		return string(EntryMarketStateTrending)
	}
	return string(EntryMarketStateChoppy)
}

// ── Exits (1m close, index levels) ──

func (s *Nifty50SR) evaluateExit(candle model.TFCandle, st *nifty50SRState) *Signal {
	if st.Side == SideNone {
		return nil
	}
	close := candle.Close
	sign := int64(1)
	if st.Side == SidePut {
		sign = -1
	}
	gain := (close - st.IndexEntry) * sign
	if !st.Breakeven && s.cfg.BreakevenAtR > 0 && gain*100 >= st.Risk*s.cfg.BreakevenAtR {
		st.StopLevel, st.Breakeven = st.IndexEntry, true
		log.Printf("[strategy] %s: %s stop to breakeven %d (gain %d)", s.Name(), st.Side, st.IndexEntry, gain)
	}
	switch {
	case (close-st.StopLevel)*sign <= 0:
		return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s STOP stop=%d close=%d", st.Side, st.StopLevel, close))
	case (close-st.TargetLevel)*sign >= 0:
		return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s TARGET target=%d close=%d", st.Side, st.TargetLevel, close))
	}
	return nil
}

// exit closes the position, books its index result against the day's caps
// (at the last 1m close) and returns the exit signal.
func (s *Nifty50SR) exit(exch, token string, st *nifty50SRState, reason string) *Signal {
	side := st.Side
	s.book(st)
	log.Printf("[strategy] %s: %s (day pnl=%d, losses in a row=%d)", s.Name(), reason, st.DayPnLPts, st.ConsecLosses)
	s.resetPosition(st)
	return &Signal{
		StrategyName: s.Name(), Action: ActionExit, Side: side,
		Token: token, Exchange: exch, Qty: s.qty, Price: st.LastClose, Reason: reason,
	}
}

func (s *Nifty50SR) book(st *nifty50SRState) {
	if st.Side == SideNone || st.LastClose <= 0 {
		return
	}
	pnl := st.LastClose - st.IndexEntry
	if st.Side == SidePut {
		pnl = -pnl
	}
	st.DayPnLPts += pnl
	if pnl < 0 {
		st.ConsecLosses++
	} else {
		st.ConsecLosses = 0
	}
}

// ── Premium stops (ticks) ──

func (s *Nifty50SR) OnTick(tick model.Tick) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	tickKey := tick.Exchange + ":" + tick.Token
	if s.cfg.VolumeToken != "" && tickKey == s.cfg.VolumeToken {
		if minute, vol, ok := s.vol.observe(tick.CanonicalTS(), tick.DayVolume); ok {
			for _, st := range s.instruments {
				st.ctx.rc.addVolume(minute, vol)
			}
		}
		return nil
	}
	for key, st := range s.instruments {
		if st.Side == SideNone || st.FNOEntryPrice <= 0 {
			continue
		}
		if held := s.heldToken(st); held != "" && tickKey == held {
			return s.checkPremium(key, tick, st)
		}
	}
	return nil
}

func (s *Nifty50SR) heldToken(st *nifty50SRState) string {
	if st.FNOToken != "" {
		return st.FNOToken
	}
	if st.Side == SideCall {
		return s.cfg.FNOCallToken
	}
	return s.cfg.FNOPutToken
}

func (s *Nifty50SR) checkPremium(key string, tick model.Tick, st *nifty50SRState) *Signal {
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
	case s.cfg.FNOTrailSLPct > 0 && (best-entry)*100 >= entry*s.cfg.FNOTrailStartPct &&
		(best-price)*100 >= best*s.cfg.FNOTrailSLPct:
		reason = fmt.Sprintf("SR %s FNO TRAIL SL premium=%d best=%d", st.Side, price, best)
	case s.cfg.FNOHardSLPct > 0 && (entry-price)*100 >= entry*s.cfg.FNOHardSLPct:
		reason = fmt.Sprintf("SR %s FNO HARD SL premium=%d entry=%d", st.Side, price, entry)
	default:
		return nil
	}
	exch, token := splitKey(key)
	sig := s.exit(exch, token, st, reason)
	sig.Price = 0 // the engine prices exits from the option LTP
	return sig
}

// ── Helpers ──

func (s *Nifty50SR) reject(reason string) {
	if s.rejects == nil {
		s.rejects = make(map[string]int)
	}
	s.rejects[reason]++
}

// RejectStats returns how often each filter refused a candidate entry bar.
func (s *Nifty50SR) RejectStats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.rejects))
	for k, v := range s.rejects {
		out[k] = v
	}
	return out
}

func (s *Nifty50SR) getOrCreate(key string) *nifty50SRState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50SRState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50SR) resetPosition(st *nifty50SRState) {
	st.Side, st.Kind, st.Regime = SideNone, "", SRRegimeNone
	st.IndexEntry, st.StopLevel, st.TargetLevel, st.Risk = 0, 0, 0, 0
	st.Breakeven = false
	st.Strike, st.FNOToken, st.FNOEntryPrice, st.FNOBestPrice = 0, "", 0, 0
	st.CooldownLeft = s.cfg.CooldownCandles
}

func (s *Nifty50SR) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		s.resetPosition(st)
		st.CooldownLeft = 0
		st.PendingSide = SideNone
	}
}

func (s *Nifty50SR) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sigs []Signal
	for key, st := range s.instruments {
		if st.Side == SideNone {
			continue
		}
		exch, token := splitKey(key)
		sig := s.exit(exch, token, st, reason)
		sig.Price = 0
		sigs = append(sigs, *sig)
	}
	return sigs
}

func (s *Nifty50SR) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50SRSnapshot(s.Name(), s.instruments)
}

func (s *Nifty50SR) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var snap nifty50SRStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	if snap.Version != nifty50SRSnapshotVersion {
		log.Printf("[strategy] %s: dropping v%d snapshot (want v%d)", s.Name(), snap.Version, nifty50SRSnapshotVersion)
		return nil
	}
	m := make(map[string]*nifty50SRState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50SR(s.cfg, is)
	}
	s.instruments = m
	return nil
}
