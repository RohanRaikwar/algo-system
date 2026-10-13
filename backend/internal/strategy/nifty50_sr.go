package strategy

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
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

	// Today's refusals for the dashboard (RejectStats keeps the run total).
	dayRejects   map[string]int
	dayWarns     map[string]int // today's entries taken with each warning
	lastReject   string
	lastRejectTS time.Time // bucket start of the candle that was refused
	rejectDay    string
	barTS        time.Time // candle being evaluated
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
	s.barTS = candle.TS
	if s.rejectDay != day {
		s.rejectDay, s.dayRejects, s.dayWarns, s.lastReject, s.lastRejectTS = day, nil, nil, "", time.Time{}
	}
	if st.TradeDay != day {
		st.TradeDay, st.TradesToday, st.ConsecLosses, st.DayPnLPts = day, 0, 0, 0
		st.LastSide, st.LastADX = SideNone, 0
		st.PendingSide = SideNone
		st.AdviceToday, st.AdviceCooldown = 0, 0
	}
	if st.CooldownLeft > 0 {
		st.CooldownLeft--
	}
	if st.AdviceCooldown > 0 {
		st.AdviceCooldown--
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

// weakReentry reports a same-side re-entry on a trend no stronger than at
// the day's previous entry.
func (s *Nifty50SR) weakReentry(st *nifty50SRState, side PositionSide, adx float64) bool {
	if s.cfg.ReentryMinADX <= 0 || st.LastSide != side {
		return false
	}
	return adx < s.cfg.ReentryMinADX && adx <= st.LastADX
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

// srDayPos is where a price stands in today's data. Distances are
// price − reference, paise.
type srDayPos struct {
	Known    bool  // day high > day low
	Pct      int64 // 0 = day low, 100 = day high
	FromHigh int64 // day high − price (≥ 0)
	FromLow  int64 // price − day low (≥ 0)
	VsPDH    int64
	VsPDL    int64
	VsOpen   int64
	HasPD    bool
	HasOpen  bool
	OR       string // above | inside | below, "" before the opening range completes
}

func dayPosition(ss SRState, price int64) srDayPos {
	d := srDayPos{}
	if rng := ss.DayHigh - ss.DayLow; ss.DayLow > 0 && rng > 0 {
		d.Known = true
		d.FromHigh, d.FromLow = ss.DayHigh-price, price-ss.DayLow
		d.Pct = d.FromLow * 100 / rng
	}
	if ss.PrevDayHigh > 0 && ss.PrevDayLow > 0 {
		d.HasPD, d.VsPDH, d.VsPDL = true, price-ss.PrevDayHigh, price-ss.PrevDayLow
	}
	if ss.DayOpen > 0 {
		d.HasOpen, d.VsOpen = true, price-ss.DayOpen
	}
	if ss.ORHigh > 0 && ss.ORLow > 0 {
		switch {
		case price > ss.ORHigh:
			d.OR = "above"
		case price < ss.ORLow:
			d.OR = "below"
		default:
			d.OR = "inside"
		}
	}
	return d
}

// String is the day picture for an order reason, index points:
// "day=pos:92% hi:-8 lo:+112 pdh:+15 pdl:+230 open:+95 or:above".
func (d srDayPos) String() string {
	if !d.Known {
		return "day=unknown"
	}
	p := func(paise int64) string { return fmt.Sprintf("%+d", paise/100) }
	out := fmt.Sprintf("day=pos:%d%% hi:%s lo:%s", d.Pct, p(-d.FromHigh), p(d.FromLow))
	if d.HasPD {
		out += " pdh:" + p(d.VsPDH) + " pdl:" + p(d.VsPDL)
	}
	if d.HasOpen {
		out += " open:" + p(d.VsOpen)
	}
	if d.OR != "" {
		out += " or:" + d.OR
	}
	return out
}

// dayExtreme reports a CALL at the top of a day that already ran up
// DayRunPts from its low (with DayExtremePuts, a PUT at the bottom of one
// that fell as far).
func (s *Nifty50SR) dayExtreme(d srDayPos, side PositionSide) bool {
	if s.cfg.DayExtremePct <= 0 || !d.Known {
		return false
	}
	if side == SideCall {
		return d.Pct >= s.cfg.DayExtremePct && d.FromLow >= s.cfg.DayRunPts
	}
	return s.cfg.DayExtremePuts && d.Pct <= 100-s.cfg.DayExtremePct && d.FromHigh >= s.cfg.DayRunPts
}

// situationWarnings names the situation checks an entry on side fails
// now: "sideways" (pullback/retest in a sideways box; fades too with
// BoxFadeToo) and "day_extreme". Empty when none apply.
func (s *Nifty50SR) situationWarnings(st *nifty50SRState, ss SRState, side PositionSide, kind string, day srDayPos, closeMin int) []string {
	var w []string
	if _, boxed := s.sideways(st, ss); boxed && (kind != SRKindFade || s.cfg.BoxFadeToo) {
		w = append(w, "sideways")
	}
	if closeMin >= s.cfg.DayExtremeFromMin && s.dayExtreme(day, side) {
		w = append(w, "day_extreme")
	}
	return w
}

// sideways returns the high−low span of the last BoxBars closed 5m bars
// and whether it is narrow enough (≤ BoxATRPct % of the 15m ATR) to
// count as a sideways box.
func (s *Nifty50SR) sideways(st *nifty50SRState, ss SRState) (int64, bool) {
	n := s.cfg.BoxBars
	bars := st.ctx.rc.bars5
	if n <= 0 || s.cfg.BoxATRPct <= 0 || len(bars) < n || ss.Range.ATR15 <= 0 {
		return 0, false
	}
	hi, lo := bars[len(bars)-n].High, bars[len(bars)-n].Low
	for _, b := range bars[len(bars)-n+1:] {
		hi, lo = maxInt64(hi, b.High), minInt64(lo, b.Low)
	}
	box := hi - lo
	return box, box*100 <= ss.Range.ATR15*s.cfg.BoxATRPct
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
	touchTol := tol
	if s.cfg.RetestTolPts > 0 {
		touchTol = s.cfg.RetestTolPts
	}
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
		touched = cur.Low <= l+touchTol && cur.Close > l
		rejected = bullishReversal(prev, cur) || (cur.bullish() && cur.lowerWick() >= cur.body())
		c = confirmations{
			VWAP: cur.Close > ss.VWAP, EMA: ss.EMAFast > ss.EMASlow,
			RSI: ss.Range.RSIReady && ss.Range.RSI5 > 50, Candle: rejected,
		}
	} else {
		touched = cur.High >= l-touchTol && cur.Close < l
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
	if sig := s.evaluateContinue(candle, st, ss, cur, closeMin); sig != nil {
		st.PendingSide = SideNone
		return sig
	}
	if st.PendingLeft <= 0 {
		expire("expired")
	}
	return nil
}

// evaluateContinue advises a break that is still holding beyond its level
// AdviceContinueMin minutes after it armed, without having retested it.
// Checked once per armed break: the bar on which the minutes are reached.
func (s *Nifty50SR) evaluateContinue(candle model.TFCandle, st *nifty50SRState, ss SRState, cur ohlcv, closeMin int) *Signal {
	if s.cfg.AdviceContinueMin <= 0 {
		return nil
	}
	wait := s.cfg.AdviceContinueMin / s.entryTF()
	if wait < 1 {
		wait = 1
	}
	if s.retestBars()-st.PendingLeft != wait {
		return nil
	}
	side, l := st.PendingSide, st.PendingLevel
	long := side == SideCall
	beyond := ss.Range.ATR15 * s.cfg.BreakATRPct / 100
	holding := (long && cur.bullish() && cur.Close >= l+beyond && cur.Close > ss.VWAP) ||
		(!long && cur.bearish() && cur.Close <= l-beyond && cur.Close < ss.VWAP)
	opposite := (long && ss.Regime == SRRegimeTrendDown) || (!long && ss.Regime == SRRegimeTrendUp)
	if !holding || opposite {
		s.reject("continue:not_holding")
		return nil
	}
	buf := s.stopBuffer(ss)
	stop, target := l-buf, levelAbove(ss.Levels, cur.Close+s.cfg.TouchTolPts)
	if !long {
		stop, target = l+buf, levelBelow(ss.Levels, cur.Close-s.cfg.TouchTolPts)
	}
	if s.cfg.AdviceMaxRiskATRPct > 0 && absInt64(cur.Close-stop)*100 > ss.Range.ATR15*s.cfg.AdviceMaxRiskATRPct {
		s.reject("continue:risk")
		return nil
	}
	c := confirmations{
		VWAP: true, Candle: true,
		EMA: (long && ss.EMAFast > ss.EMASlow) || (!long && ss.EMAFast < ss.EMASlow),
		RSI: ss.Range.RSIReady && ((long && ss.Range.RSI5 > 50) || (!long && ss.Range.RSI5 < 50)),
	}
	return s.advise(candle, st, ss, side, SRKindContinue, l, stop, target, c, closeMin)
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
	kind := SRKindPullback
	if anchor == 0 && s.cfg.AdvicePullFastEMA && ss.EMAFast > 0 {
		// Shallow dip in a strong trend: advice only (see advise).
		if a := ss.EMAFast; (long && cur.Low <= a+tol && close > a) || (!long && cur.High >= a-tol && close < a) {
			anchor, kind = a, SRKindPullbackFast
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
			s.reject(kindTag(kind) + ":no_close_back")
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
			s.reject(kindTag(kind) + ":no_close_back")
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
		s.reject(kindTag(kind) + ":level_in_way")
		return nil
	}
	if kind == SRKindPullbackFast {
		return s.advise(candle, st, ss, side, kind, anchor, stop, target, c, closeMin)
	}
	return s.tryEnter(candle, st, ss, side, kind, anchor, stop, target, c, closeMin)
}

// tryEnter applies confirmations, caps and reward:risk, then opens the
// position. target 0 means "no next level": DefaultTargetR is used.
func (s *Nifty50SR) tryEnter(candle model.TFCandle, st *nifty50SRState, ss SRState, side PositionSide, kind string,
	level, stop, target int64, c confirmations, closeMin int) *Signal {
	p, ok := s.checkEntry(candle, st, ss, side, kind, stop, target, c, closeMin)
	if !ok {
		return nil
	}
	return s.open(candle, st, ss, side, kind, level, c, p)
}

// entryPlan is a checked entry: prices, the day position and warnings.
type entryPlan struct {
	entry, stop, target, risk, reward int64
	day                               srDayPos
	warns                             []string
}

// checkEntry applies confirmations, caps, the situation gate and
// reward:risk. target 0 means "no next level": DefaultTargetR is used.
func (s *Nifty50SR) checkEntry(candle model.TFCandle, st *nifty50SRState, ss SRState, side PositionSide, kind string,
	stop, target int64, c confirmations, closeMin int) (entryPlan, bool) {
	fail := func(reason string) (entryPlan, bool) {
		s.reject(reason)
		return entryPlan{}, false
	}
	if n := c.count(); n < s.cfg.MinConfirmations {
		return fail(fmt.Sprintf("%s:confirmations_%d", kindTag(kind), n))
	}
	if why := s.entryBlock(st, closeMin); why != "" {
		return fail(why)
	}
	if s.weakReentry(st, side, ss.Range.ADX) {
		return fail(kindTag(kind) + ":reentry_adx")
	}
	day := dayPosition(ss, candle.Close)
	warns := s.situationWarnings(st, ss, side, kind, day, closeMin)
	if s.cfg.GateMode == SRGateBlock && len(warns) > 0 {
		return fail(kindTag(kind) + ":" + warns[0])
	}
	entry := candle.Close
	risk := absInt64(entry - stop)
	if risk <= 0 || (side == SideCall && stop >= entry) || (side == SidePut && stop <= entry) {
		return fail(kindTag(kind) + ":bad_stop")
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
		return fail(kindTag(kind) + ":reward_risk")
	}
	return entryPlan{entry: entry, stop: stop, target: target, risk: risk, reward: reward, day: day, warns: warns}, true
}

// open records the position and returns its BUY signal.
func (s *Nifty50SR) open(candle model.TFCandle, st *nifty50SRState, ss SRState, side PositionSide, kind string,
	level int64, c confirmations, p entryPlan) *Signal {
	entry, stop, target, risk, reward, warns := p.entry, p.stop, p.target, p.risk, p.reward, p.warns

	st.Side, st.Kind, st.Regime = side, kind, ss.Regime
	st.IndexEntry, st.StopLevel, st.TargetLevel, st.Risk = entry, stop, target, risk
	st.Breakeven, st.SupportLvl, st.BreakBars = false, 0, 0
	st.EntryTS, st.MaxGain = candle.TS, 0
	st.LastSide, st.LastADX = side, ss.Range.ADX
	st.FNOToken, st.FNOEntryPrice, st.FNOBestPrice = "", 0, 0
	st.Strike = atmStrike(entry, s.cfg.StrikeStep)
	st.TradesToday++

	reason := s.entryReason(ss, side, kind, level, c, p, st.Strike)
	if len(warns) > 0 {
		reason += " warn=" + strings.Join(warns, ",")
		if s.dayWarns == nil {
			s.dayWarns = make(map[string]int)
		}
		for _, w := range warns {
			s.dayWarns[w]++
		}
	}
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	return &Signal{
		StrategyName: s.Name(), Action: ActionBuy, Side: side,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: srMarketState(ss.Regime), Reason: reason, Strike: st.Strike,
		TargetMove: reward,
		Warnings:   warns,
	}
}

// entryReason is the signal reason shared by entries and entry advice.
func (s *Nifty50SR) entryReason(ss SRState, side PositionSide, kind string, level int64, c confirmations, p entryPlan, strike int64) string {
	return fmt.Sprintf("SR %s %s regime=%s level=%d stop=%d target=%d vwap=%d ema=%d/%d rsi5=%.1f adx=%.1f %s %s strike=%d close=%d",
		kind, side, ss.Regime, level, p.stop, p.target, ss.VWAP, ss.EMAFast, ss.EMASlow, ss.Range.RSI5, ss.Range.ADX, c, p.day, strike, p.entry)
}

// advise returns entry advice (WATCH_BUY) for an advice-only setup. It runs
// the real entry checks plus a strong-trend guard (ADX ≥ StrongTrendADX,
// no sideways box, whatever GateMode says) and its own cooldown and daily
// cap. It never opens a position, so real SR entries, caps and P&L are
// untouched; stratengine publishes the signal and places no order.
func (s *Nifty50SR) advise(candle model.TFCandle, st *nifty50SRState, ss SRState, side PositionSide, kind string,
	level, stop, target int64, c confirmations, closeMin int) *Signal {
	if s.cfg.AdviceAsTrades {
		return s.tryEnter(candle, st, ss, side, kind, level, stop, target, c, closeMin)
	}
	tag := kindTag(kind)
	if ss.Range.ADX < s.cfg.StrongTrendADX {
		s.reject(tag + ":weak_adx")
		return nil
	}
	if _, boxed := s.sideways(st, ss); boxed {
		s.reject(tag + ":sideways")
		return nil
	}
	if st.AdviceCooldown > 0 {
		s.reject(tag + ":advice_cooldown")
		return nil
	}
	if st.AdviceToday >= s.cfg.MaxTradesPerDay {
		s.reject(tag + ":advice_cap")
		return nil
	}
	p, ok := s.checkEntry(candle, st, ss, side, kind, stop, target, c, closeMin)
	if !ok {
		return nil
	}
	st.AdviceToday++
	st.AdviceCooldown = s.cfg.CooldownCandles
	strike := atmStrike(p.entry, s.cfg.StrikeStep)
	reason := s.entryReason(ss, side, kind, level, c, p, strike)
	if len(p.warns) > 0 {
		reason += " warn=" + strings.Join(p.warns, ",")
	}
	log.Printf("[strategy] %s: ADVICE %s", s.Name(), reason)
	return &Signal{
		StrategyName: s.Name(), Action: ActionWatchBuy, Side: side,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: srMarketState(ss.Regime), Reason: reason, Strike: strike,
		TargetMove: p.reward,
		Warnings:   p.warns,
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
	case SRKindPullbackFast:
		return "pullback_fast"
	case SRKindContinue:
		return "continue"
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
	if gain > st.MaxGain {
		st.MaxGain = gain
	}
	support := s.supportStopOn()
	switch {
	case !st.Breakeven && s.cfg.BreakevenAtR > 0 && gain*100 >= st.Risk*s.cfg.BreakevenAtR:
		st.Breakeven = true
		if support {
			s.raiseSupportStop(st, close)
		} else {
			st.StopLevel = st.IndexEntry
			log.Printf("[strategy] %s: %s stop to breakeven %d (gain %d)", s.Name(), st.Side, st.IndexEntry, gain)
		}
	case st.Breakeven && support && s.cfg.SupportStopMode == "trail":
		s.raiseSupportStop(st, close)
	}
	beyond := (close-st.StopLevel)*sign <= 0
	if beyond && st.SupportLvl != 0 {
		st.BreakBars++
		beyond = st.BreakBars >= s.cfg.SupportStopConfirm
	} else if !beyond {
		st.BreakBars = 0
	}
	switch {
	case beyond:
		if st.SupportLvl != 0 {
			return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s STOP stop=%d close=%d support=%d broke",
				st.Side, st.StopLevel, close, st.SupportLvl))
		}
		return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s STOP stop=%d close=%d", st.Side, st.StopLevel, close))
	case (close-st.TargetLevel)*sign >= 0:
		return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s TARGET target=%d close=%d", st.Side, st.TargetLevel, close))
	case s.noProgress(candle, st):
		return s.exit(candle.Exchange, candle.Token, st, fmt.Sprintf("SR %s NO PROGRESS %dm best=%d need=%d close=%d",
			st.Side, s.cfg.NoProgressMin, st.MaxGain, st.Risk*s.cfg.NoProgressRPct/100, close))
	}
	return nil
}

// noProgress reports a trade that, NoProgressMin minutes after entry, has
// never closed NoProgressRPct of R in its favour. A position restored
// without its entry time is left alone.
func (s *Nifty50SR) noProgress(candle model.TFCandle, st *nifty50SRState) bool {
	if s.cfg.NoProgressMin <= 0 || st.EntryTS.IsZero() || st.Breakeven {
		return false
	}
	if candle.TS.Sub(st.EntryTS) < time.Duration(s.cfg.NoProgressMin)*time.Minute {
		return false
	}
	return st.MaxGain*100 < st.Risk*s.cfg.NoProgressRPct
}

// exit closes the position, books its index result against the day's caps
// (at the last 1m close) and returns the exit signal.
func (s *Nifty50SR) supportStopOn() bool {
	m := s.cfg.SupportStopMode
	return m == "breakeven" || m == "trail"
}

// raiseSupportStop moves the stop under the nearest strong support below
// close (above it for a PUT), zone points beyond the level. A dip into the
// zone is the level holding; the stop only ever tightens, and with no
// strong level the stop stays where it is.
func (s *Nifty50SR) raiseSupportStop(st *nifty50SRState, close int64) {
	ss := st.ctx.State()
	zone := s.cfg.SupportStopZonePts
	if s.cfg.SupportStopZoneATRPct > 0 && ss.Range.ATR15 > 0 {
		zone = ss.Range.ATR15 * s.cfg.SupportStopZoneATRPct / 100
	}
	var lvl, stop int64
	if st.Side == SideCall {
		if lvl = strongLevelBelow(ss.Levels, close, s.cfg.SupportStopTouches); lvl == 0 {
			return
		}
		if stop = lvl - zone; stop <= st.StopLevel {
			return
		}
	} else {
		if lvl = strongLevelAbove(ss.Levels, close, s.cfg.SupportStopTouches); lvl == 0 {
			return
		}
		if stop = lvl + zone; stop >= st.StopLevel {
			return
		}
	}
	st.StopLevel, st.SupportLvl, st.BreakBars = stop, lvl, 0
	log.Printf("[strategy] %s: %s stop to %d under level %d (zone %d)", s.Name(), st.Side, stop, lvl, zone)
}

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
	if s.dayRejects == nil {
		s.dayRejects = make(map[string]int)
	}
	s.dayRejects[reason]++
	s.lastReject, s.lastRejectTS = reason, s.barTS
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
	st.Breakeven, st.SupportLvl, st.BreakBars = false, 0, 0
	st.EntryTS, st.MaxGain = time.Time{}, 0
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
	return s.forceExit("", reason)
}

// ForceExitStale exits only positions whose last bar was on a day other
// than today (IST "2006-01-02"), so a restart mid-session keeps today's
// positions open.
func (s *Nifty50SR) ForceExitStale(today, reason string) []Signal {
	return s.forceExit(today, reason)
}

// forceExit exits every open position, or with today set, only those not
// from today.
// ExitRequested closes the open position on side for an outside reason
// (exitwatch). fnoToken, when both sides know it, must be the contract
// held, so a request for an earlier trade cannot close a new one. nil
// when no such position is open.
func (s *Nifty50SR) ExitRequested(side PositionSide, fnoToken, reason string) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, st := range s.instruments {
		if st.Side != side {
			continue
		}
		if fnoToken != "" && st.FNOToken != "" && st.FNOToken != fnoToken && !strings.HasSuffix(st.FNOToken, ":"+fnoToken) {
			log.Printf("[strategy] %s: exit request for %s %s ignored — position holds %s", s.Name(), side, fnoToken, st.FNOToken)
			return nil
		}
		exch, token := splitKey(key)
		sig := s.exit(exch, token, st, reason)
		sig.Price = 0 // the engine prices exits from the option LTP
		return sig
	}
	return nil
}

// ProtectArmed reports whether the open position on side has its stop at
// breakeven (the trade is working).
func (s *Nifty50SR) ProtectArmed(side PositionSide) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side == side {
			return st.Breakeven
		}
	}
	return false
}

func (s *Nifty50SR) forceExit(today, reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sigs []Signal
	for key, st := range s.instruments {
		if st.Side == SideNone || (today != "" && st.TradeDay == today) {
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
