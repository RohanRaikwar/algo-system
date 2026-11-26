package strategy

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// TelegramSignalName is the strategy name of trades taken from Telegram
// calls. It is paper unless STRAT_LIVE_STRATEGIES names it. Channels with
// their own trade (STRAT_TG_CHANNELS) run under names starting with
// TelegramChannelPrefix, e.g. TG_VIKASH.
const (
	TelegramSignalName    = "TELEGRAM_SIGNAL"
	TelegramChannelPrefix = "TG_"
)

// IsTelegramStrategy reports whether name is a Telegram call strategy.
func IsTelegramStrategy(name string) bool {
	return name == TelegramSignalName || strings.HasPrefix(name, TelegramChannelPrefix)
}

// TelegramSignalConfig tunes TelegramSignal.
type TelegramSignalConfig struct {
	// TargetPct is our own premium target above the entry, % (0 = none).
	// With CallTargets it applies only to a call without targets.
	TargetPct int64
	// CallTargets trades the call's targets: T1ExitLots are sold at the
	// first, the rest at the last. Off = the targets only drive the stop
	// ladder.
	CallTargets bool
	// Lots is the size of each trade in lots (paper; 0 = the executor's
	// default size). T1ExitLots of them are sold at the first target; the
	// rest ride to the last target or the stop ladder. With only one
	// target, or no more lots than T1ExitLots, all exit at the first.
	Lots, T1ExitLots int64
	// MaxChasePct refuses an entry whose premium is already more than this
	// % above the call's trigger. Unused when EntryCapPaise is set.
	MaxChasePct int64
	// EntryCapPaise, when > 0, buys with one exchange order capped at the
	// call's trigger + this: a STOPLOSS_LIMIT BUY the exchange fires at the
	// trigger while the premium is below it, else a LIMIT at the cap that
	// rests until it fills, an exit cancels it or the arm window ends. The
	// order is never chased above the cap.
	EntryCapPaise int64
	// MaxPriceGapPct drops a call whose first seen premium is more than
	// this % away from its trigger: the call was misread or is for another
	// contract. 0 = off.
	MaxPriceGapPct int64
	// ArmFor is how long a call waits for its trigger before it is dropped.
	ArmFor time.Duration
	// LadderTrailPct is the trail distance (% below the best premium) once
	// the price is past the caller's last target. The ladder below it: T1
	// moves the stop to our entry, each later target to the one before.
	LadderTrailPct int64
	// WaitActive: the caller confirms each call ("Active", or a price post
	// at the entry; tgsignal sends TGActivate). A call never buys on price;
	// it buys on that confirmation, within MaxChasePct above the entry, and
	// is dropped if it does not come within ArmFor.
	WaitActive bool
}

// TGArm is a call waiting for its trigger, with its contract resolved.
type TGArm struct {
	Call          model.TGSignal
	IndexToken    string
	IndexExchange string
	FNOExchange   string // NFO, BFO
	FNOToken      string // bare option token
	FNOSymbol     string
	// Qty is the signal quantity for the contract's underlying (SENSEX:
	// lots × 20); 0 = the strategy's qty (NIFTY STRAT_QTY).
	Qty     int64
	ArmedAt time.Time
	Active  bool  // WaitActive: the caller said "Active", buy on the next price
	Last    int64 // last premium seen (paise), 0 = none yet
	// AboveSL: a premium above the call's stop was seen. Only then does a
	// fall to the stop drop the call; a premium that starts below the stop
	// is a breakout call ("buy above 130, SL 122" at 99) and keeps waiting.
	AboveSL bool
	Seen    bool // a premium tick was checked against the trigger
	Ticked  bool // FIRST_TICK reported
}

func (a TGArm) key() string { return a.FNOExchange + ":" + a.FNOToken }

// TGPosition is the open trade. It is persisted by stratengine so a
// restart keeps the call's stop.
type TGPosition struct {
	Arm       TGArm     `json:"arm"`
	Entry     int64     `json:"entry"`             // option premium at entry (paise)
	SL        int64     `json:"sl"`                // stop (paise): the call's, raised by follow-ups
	Target    int64     `json:"target"`            // our target (paise), 0 = none
	Last      int64     `json:"last"`              // last premium seen (paise)
	Best      int64     `json:"best"`              // highest premium since entry (paise)
	Targets   []int64   `json:"targets,omitempty"` // the caller's targets (paise): the stop ladder
	EnteredTS time.Time `json:"entered_ts"`
	ManualSL  bool      `json:"manual_sl,omitempty"` // SL was set by hand (dashboard)
	// Resting: bought with an exchange order (EntryCapPaise) that has not
	// filled yet; FillEntry clears it. False for older saved trades.
	Resting bool `json:"resting,omitempty"`
	// Lots bought and still held; T1Done once the partial exit at the
	// first target was sent. Lots 0 = an older saved trade (no partial).
	Lots     int64 `json:"lots,omitempty"`
	LotsLeft int64 `json:"lots_left,omitempty"`
	T1Done   bool  `json:"t1_done,omitempty"`
}

// TGPartialExit is the event of the lots sold at the first target.
const TGPartialExit = "PARTIAL_EXIT"

// Arm results.
const (
	TGArmed       = "ARMED"
	TGReplaced    = "REPLACED"     // same message or contract was waiting
	TGSLRaised    = "SL_RAISED"    // edit of the held call raised its stop
	TGEditIgnored = "EDIT_IGNORED" // edit of the held call that would add risk
	TGLevelsSet   = "LEVELS_SET"   // stop/target set by hand (dashboard)
)

// TGFirstTick is the first premium tick of an armed call's contract: the
// subscription works and the feed delivers.
const TGFirstTick = "FIRST_TICK"

// firstTickDetail names the premium and the feed's lag (exchange time to
// arrival) when both stamps are known.
func firstTickDetail(t model.Tick) string {
	d := fmt.Sprintf("premium=%d", t.Price)
	if !t.EventTS.IsZero() && !t.TickTS.IsZero() {
		d += fmt.Sprintf(" feed lag %dms", t.TickTS.Sub(t.EventTS).Milliseconds())
	}
	return d
}

// TelegramSignal trades option calls from a Telegram group: it buys the
// named contract when its premium reaches the call's trigger, exits at the
// call's stop, at its own target, or when the caller's reply says so. One
// trade at a time.
type TelegramSignal struct {
	mu   sync.Mutex
	name string
	qty  int64
	cfg  TelegramSignalConfig
	arms []TGArm
	pos  *TGPosition

	// onEvent reports what happened to a call (callKey = TGSignal.Key).
	// Called with s.mu held: it must not block or call back.
	onEvent func(callKey, event, detail string)
	// onDrop reports a call dropped before entry (chart marker). Called
	// with s.mu held: it must not block or call back.
	onDrop func(a TGArm, reason string)
}

// NewTelegramSignal builds the strategy.
// name "" = TelegramSignalName.
func NewTelegramSignal(name string, qty int64, cfg TelegramSignalConfig) *TelegramSignal {
	if name == "" {
		name = TelegramSignalName
	}
	if cfg.ArmFor <= 0 {
		cfg.ArmFor = 30 * time.Minute
	}
	return &TelegramSignal{name: name, qty: qty, cfg: cfg}
}

// SetOnEvent sets the event callback (review log). It must not block.
func (s *TelegramSignal) SetOnEvent(f func(callKey, event, detail string)) {
	s.mu.Lock()
	s.onEvent = f
	s.mu.Unlock()
}

// SetOnDrop sets the callback for calls dropped before entry. It must not
// block.
func (s *TelegramSignal) SetOnDrop(f func(a TGArm, reason string)) {
	s.mu.Lock()
	s.onDrop = f
	s.mu.Unlock()
}

// drop reports a call that leaves the arms without an entry.
func (s *TelegramSignal) drop(a TGArm, reason string) {
	s.event(a.Call.Key(), "DROPPED", reason)
	if s.onDrop != nil {
		s.onDrop(a, reason)
	}
}

func (s *TelegramSignal) event(callKey, event, detail string) {
	log.Printf("[strategy] %s: call %s %s %s", s.Name(), callKey, event, detail)
	if s.onEvent != nil {
		s.onEvent(callKey, event, detail)
	}
}

func (s *TelegramSignal) Name() string { return s.name }

// qtyOf is a's signal quantity: its own, else the strategy's.
func (s *TelegramSignal) qtyOf(a TGArm) int64 {
	if a.Qty > 0 {
		return a.Qty
	}
	return s.qty
}

func (s *TelegramSignal) OnTFCandle(model.TFCandle) *Signal { return nil }

// Arm queues a call. A call from the same message (an edit or repeat) or
// for the same contract replaces the waiting one. An edit of the call we
// hold only raises its stop.
func (s *TelegramSignal) Arm(a TGArm) (string, error) {
	if err := a.Call.Validate(); err != nil {
		return "", err
	}
	if a.FNOToken == "" || a.FNOExchange == "" {
		return "", fmt.Errorf("no contract")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.pos; p != nil {
		if a.Call.Edited && p.Arm.Call.Key() == a.Call.Key() {
			if d := callChange(p.Arm, a); d != "" {
				// Contract or entry changed on a bought call: keep the
				// position, tell the operator (an unfilled one is
				// replaced earlier, by ReplaceResting).
				return TGEditIgnored, fmt.Errorf("caller edited the held call (%s); position kept", d)
			}
			return s.raiseSL(p, a.Call.SLPaise, "edited call")
		}
		return "", fmt.Errorf("position open in %s", p.Arm.FNOSymbol)
	}
	for i := range s.arms {
		if s.arms[i].Call.Key() == a.Call.Key() || s.arms[i].key() == a.key() {
			s.arms[i] = a
			return TGReplaced, nil
		}
	}
	s.arms = append(s.arms, a)
	return TGArmed, nil
}

// callChange names what an edit changed in the call's contract or entry,
// "" when only the stop or targets changed.
func callChange(old, edited TGArm) string {
	switch {
	case old.key() != edited.key():
		return fmt.Sprintf("contract %s → %s", old.FNOSymbol, edited.FNOSymbol)
	case old.Call.TriggerPaise != edited.Call.TriggerPaise:
		return fmt.Sprintf("entry %d → %d", old.Call.TriggerPaise, edited.Call.TriggerPaise)
	}
	return ""
}

// ReplaceResting handles an edit of the call whose entry order is still
// resting unfilled, when the edit changes its contract or entry: the
// entry is exited (the executor cancels the resting order; anything that
// filled first is sold) and the edited call waits for a new "Active". It
// returns that exit signal, or nil when the edit is not such a change.
func (s *TelegramSignal) ReplaceResting(a TGArm) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pos
	if p == nil || !p.Resting || !a.Call.Edited || p.Arm.Call.Key() != a.Call.Key() {
		return nil
	}
	d := callChange(p.Arm, a)
	if d == "" {
		return nil
	}
	reason := "TG CALL EDITED before fill: " + d
	s.event(p.Arm.Call.Key(), TGReplaced, reason+"; entry order cancelled, waiting for a new Active")
	sig := s.exit(reason)
	s.arms = append(s.arms, a)
	return sig
}

// raiseSL moves the held stop up to sl when that lowers risk: above the
// current stop and below the last premium. The caller holds s.mu.
func (s *TelegramSignal) raiseSL(p *TGPosition, sl int64, why string) (string, error) {
	switch {
	case sl <= p.SL:
		return TGEditIgnored, fmt.Errorf("%s: stop %d not above current %d", why, sl, p.SL)
	case p.Last > 0 && sl >= p.Last:
		return TGEditIgnored, fmt.Errorf("%s: stop %d not below premium %d", why, sl, p.Last)
	}
	old := p.SL
	p.SL, p.ManualSL = sl, false
	s.event(p.Arm.Call.Key(), TGSLRaised, fmt.Sprintf("%s: stop %d → %d", why, old, sl))
	return TGSLRaised, nil
}

// SetLevels applies a dashboard edit to the open trade's premium stop and
// target. Index levels are refused: a call's levels are premiums. A stop
// must sit below the last premium and a target above it, or the edit is
// refused whole. A hand stop may be lower than the call's: the user
// decides. The ladder still raises it from there.
func (s *TelegramSignal) SetLevels(side PositionSide, fnoToken string, e LevelEdit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pos
	switch {
	case e.IndexStop != 0 || e.IndexTarget != 0 || e.IndexTrail != 0:
		return fmt.Errorf("%s levels are option premiums, not index", s.Name())
	case p == nil || p.Arm.Call.Side() != side:
		return fmt.Errorf("no open %s %s position", s.Name(), side)
	case fnoToken != "" && p.Arm.FNOToken != fnoToken && !strings.HasSuffix(fnoToken, ":"+p.Arm.FNOToken):
		return fmt.Errorf("position holds %s, not %s", p.Arm.FNOToken, fnoToken)
	}
	if e.FNOSL > 0 || e.FNOTarget > 0 {
		if p.Last <= 0 {
			return fmt.Errorf("no option price yet")
		}
		if e.FNOSL > 0 && e.FNOSL >= p.Last {
			return fmt.Errorf("premium stop %d is not below LTP %d", e.FNOSL, p.Last)
		}
		if e.FNOTarget > 0 && e.FNOTarget <= p.Last {
			return fmt.Errorf("premium target %d is not above LTP %d", e.FNOTarget, p.Last)
		}
	}
	if e.FNOSL > 0 {
		p.SL, p.ManualSL = e.FNOSL, true
	}
	switch {
	case e.FNOTarget > 0:
		p.Target = e.FNOTarget
	case e.FNOTarget < 0:
		p.Target = 0
	}
	s.event(p.Arm.Call.Key(), TGLevelsSet, fmt.Sprintf("by hand: stop %d target %d", p.SL, p.Target))
	return nil
}

// FollowUp applies the caller's message about an earlier call. It acts
// only on the call it is linked to: by reply, or, without a reply, when
// exactly one call is open and the message names its strike. It returns
// the exit to send, if any, and what was done.
func (s *TelegramSignal) FollowUp(f model.TGFollowUp) (*Signal, string, error) {
	if err := f.Validate(); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	held, armIdx, err := s.linkFollowUp(f)
	if err != nil {
		return nil, "", err
	}
	reason := fmt.Sprintf("TG CALLER %s msg=%s", strings.ToUpper(string(f.Action)), f.Key())
	if f.Action == model.TGActivate {
		return s.activate(held, armIdx, f)
	}
	switch {
	case held && f.Action == model.TGMoveSL:
		res, err := s.raiseSL(s.pos, f.NewSLPaise, "caller "+f.Key())
		return nil, res, err
	case held:
		key := s.pos.Arm.Call.Key()
		sig := s.exit(reason)
		s.event(key, "EXIT", reason)
		return sig, "EXIT", nil
	case f.Action == model.TGMoveSL:
		a := &s.arms[armIdx]
		if f.NewSLPaise <= a.Call.SLPaise || f.NewSLPaise >= a.Call.TriggerPaise {
			return nil, "", fmt.Errorf("stop %d not between current %d and entry %d", f.NewSLPaise, a.Call.SLPaise, a.Call.TriggerPaise)
		}
		a.Call.SLPaise = f.NewSLPaise
		s.event(a.Call.Key(), TGSLRaised, "waiting call, caller "+f.Key())
		return nil, TGSLRaised, nil
	default: // exit / sl_hit / target_hit / cancel before entry
		a := s.arms[armIdx]
		s.arms = append(s.arms[:armIdx], s.arms[armIdx+1:]...)
		s.drop(a, reason)
		return nil, "DROPPED", nil
	}
}

// activate applies the caller's "Active" reply. Only a WaitActive
// strategy acts on it; the others already buy on price. With an entry cap
// it buys at once, in this same step, whatever our premium feed shows:
// the caller's message is the signal. Without one it buys now with a
// premium already seen, else on the next tick. The caller holds s.mu.
func (s *TelegramSignal) activate(held bool, armIdx int, f model.TGFollowUp) (*Signal, string, error) {
	switch {
	case !s.cfg.WaitActive:
		return nil, "", fmt.Errorf("%s buys on price, not on Active", s.Name())
	case held:
		return nil, "", fmt.Errorf("call already held")
	}
	a := &s.arms[armIdx]
	a.Active = true
	s.event(a.Call.Key(), "ACTIVE", "caller "+f.Key())
	if s.cfg.EntryCapPaise > 0 {
		price := a.Last
		if price <= 0 {
			price = a.Call.TriggerPaise
		}
		return s.enter(*a, price, time.Now()), "ENTERED", nil
	}
	if a.Last <= 0 {
		return nil, "ACTIVE", nil
	}
	n := len(s.arms)
	sig := s.tryActiveEntry(armIdx, a.Last, time.Now())
	switch {
	case sig != nil:
		return sig, "ENTERED", nil
	case len(s.arms) < n:
		return nil, "DROPPED", nil
	}
	return nil, "ACTIVE", nil
}

// tryActiveEntry buys an activated call at price, or drops it when the
// premium is at its stop or ran past the chase limit. The caller holds s.mu.
func (s *TelegramSignal) tryActiveEntry(i int, price int64, now time.Time) *Signal {
	a := s.arms[i]
	c := a.Call
	drop := ""
	switch {
	case price <= c.SLPaise && a.AboveSL:
		drop = fmt.Sprintf("premium %d at stop %d when Active", price, c.SLPaise)
	case price <= c.SLPaise:
		// Never above the stop yet: buy once it is, never below it.
		s.arms[i].Seen, s.arms[i].Last = true, price
		return nil
	case s.cfg.EntryCapPaise == 0 && price*100 > c.TriggerPaise*(100+s.cfg.MaxChasePct):
		drop = fmt.Sprintf("missed: premium %d more than %d%% above entry %d when Active", price, s.cfg.MaxChasePct, c.TriggerPaise)
	}
	if drop != "" {
		s.drop(a, drop)
		s.arms = append(s.arms[:i], s.arms[i+1:]...)
		return nil
	}
	return s.enter(a, price, now)
}

// linkFollowUp finds the call f is about: held is true for the open
// position, else armIdx is the waiting call. The caller holds s.mu.
func (s *TelegramSignal) linkFollowUp(f model.TGFollowUp) (held bool, armIdx int, err error) {
	if key := f.CallKey(); key != "" {
		if s.pos != nil && s.pos.Arm.Call.Key() == key {
			return true, -1, nil
		}
		for i, a := range s.arms {
			if a.Call.Key() == key {
				return false, i, nil
			}
		}
		return false, -1, fmt.Errorf("UNLINKED: reply to %s, which is neither held nor waiting", key)
	}
	open := len(s.arms)
	if s.pos != nil {
		open++
	}
	if open != 1 {
		return false, -1, fmt.Errorf("UNLINKED: not a reply and %d calls open", open)
	}
	call := func(c model.TGSignal) error {
		if f.Strike == 0 || f.Strike != c.Strike || (f.OptionType != "" && f.OptionType != c.OptionType) {
			return fmt.Errorf("UNLINKED: not a reply and names %d%s, open call is %d%s", f.Strike, f.OptionType, c.Strike, c.OptionType)
		}
		return nil
	}
	if s.pos != nil {
		return true, -1, call(s.pos.Arm.Call)
	}
	return false, 0, call(s.arms[0].Call)
}

// Arms returns the calls waiting for their trigger.
func (s *TelegramSignal) Arms() []TGArm {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TGArm(nil), s.arms...)
}

// Position returns a copy of the open trade, or nil.
func (s *TelegramSignal) Position() *TGPosition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil {
		return nil
	}
	p := *s.pos
	return &p
}

// Restore reinstates a trade after a restart.
func (s *TelegramSignal) Restore(p TGPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pos = &p
	s.arms = nil
}

// FillEntry sets the open trade's entry to its fill price (an exchange
// order can fill below the cap), and the own target with it.
func (s *TelegramSignal) FillEntry(side PositionSide, price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil || s.pos.Arm.Call.Side() != side || price <= 0 {
		return
	}
	s.pos.Resting = false
	s.pos.Entry = price
	if s.cfg.TargetPct > 0 && !(s.cfg.CallTargets && len(s.pos.Targets) > 0) {
		s.pos.Target = price * (100 + s.cfg.TargetPct) / 100
	}
}

// CancelEntry forgets an entry stratengine refused, so the strategy does
// not think it holds a position.
func (s *TelegramSignal) CancelEntry(side PositionSide, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos != nil && s.pos.Arm.Call.Side() == side {
		s.event(s.pos.Arm.Call.Key(), "ENTRY_REFUSED", reason)
		s.pos = nil
	}
}

// ForceExitAll exits the open trade (EOD) and drops waiting calls.
func (s *TelegramSignal) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.arms {
		s.drop(a, reason)
	}
	s.arms = nil
	if s.pos == nil {
		return nil
	}
	s.event(s.pos.Arm.Call.Key(), "EXIT", reason)
	sig := s.exit(reason)
	return []Signal{*sig}
}

// ExitRequested closes the trade on side for an outside reason (dashboard).
func (s *TelegramSignal) ExitRequested(side PositionSide, fnoToken, reason string) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil || s.pos.Arm.Call.Side() != side {
		return nil
	}
	if fnoToken != "" && s.pos.Arm.FNOToken != fnoToken && !strings.HasSuffix(fnoToken, ":"+s.pos.Arm.FNOToken) {
		return nil
	}
	s.event(s.pos.Arm.Call.Key(), "EXIT", reason)
	return s.exit(reason)
}

// OnTick watches the waiting and held contracts' premiums.
func (s *TelegramSignal) OnTick(tick model.Tick) *Signal {
	price := tick.Price
	if price <= 0 {
		return nil
	}
	key := tick.Exchange + ":" + tick.Token
	now := tick.CanonicalTS()

	s.mu.Lock()
	defer s.mu.Unlock()

	if p := s.pos; p != nil {
		if key != p.Arm.key() {
			return nil
		}
		p.Last = price
		if price > p.Best {
			p.Best = price
			if stop, rung := s.ladderStop(p); stop > p.SL && stop < price {
				old := p.SL
				p.SL, p.ManualSL = stop, false
				s.event(p.Arm.Call.Key(), TGSLRaised, fmt.Sprintf("%s → stop %d (was %d)", rung, stop, old))
			}
		}
		var reason string
		switch {
		case price <= p.SL && p.ManualSL:
			reason = fmt.Sprintf("TG MANUAL STOP premium=%d stop=%d entry=%d best=%d", price, p.SL, p.Entry, p.Best)
		case price <= p.SL && p.SL > p.Arm.Call.SLPaise:
			reason = fmt.Sprintf("TG RAISED STOP premium=%d stop=%d entry=%d best=%d", price, p.SL, p.Entry, p.Best)
		case price <= p.SL:
			reason = fmt.Sprintf("TG SL premium=%d sl=%d entry=%d", price, p.SL, p.Entry)
		case s.partialDue(p, price):
			return s.partialExit(p, price)
		case p.Target > 0 && price >= p.Target:
			reason = fmt.Sprintf("TG TARGET premium=%d target=%d entry=%d", price, p.Target, p.Entry)
		default:
			return nil
		}
		s.event(p.Arm.Call.Key(), "EXIT", reason)
		return s.exit(reason)
	}

	s.expire(now)
	for i := range s.arms {
		a := &s.arms[i]
		if a.key() != key {
			continue
		}
		c := a.Call
		if !a.Ticked {
			a.Ticked = true
			s.event(c.Key(), TGFirstTick, firstTickDetail(tick))
		}
		drop := ""
		switch {
		case !a.Seen && s.cfg.MaxPriceGapPct > 0 && gapPct(price, c.TriggerPaise) > s.cfg.MaxPriceGapPct:
			drop = fmt.Sprintf("premium %d is more than %d%% from entry %d — call misread or wrong contract", price, s.cfg.MaxPriceGapPct, c.TriggerPaise)
		case s.cfg.WaitActive && a.Active:
			return s.tryActiveEntry(i, price, now)
		case price <= c.SLPaise && a.AboveSL:
			drop = fmt.Sprintf("premium %d hit stop %d before entry %d", price, c.SLPaise, c.TriggerPaise)
		case price <= c.SLPaise || price < c.TriggerPaise || s.cfg.WaitActive:
			// Below the entry (a premium still under the stop is a breakout
			// call), or waiting for the caller's "Active", not for the price.
			a.Seen, a.Last = true, price
			if price > c.SLPaise {
				a.AboveSL = true
			}
			return nil
		case s.cfg.EntryCapPaise == 0 && price*100 > c.TriggerPaise*(100+s.cfg.MaxChasePct):
			drop = fmt.Sprintf("missed: premium %d more than %d%% above entry %d", price, s.cfg.MaxChasePct, c.TriggerPaise)
		}
		if drop != "" {
			s.drop(*a, drop)
			s.arms = append(s.arms[:i], s.arms[i+1:]...)
			return nil
		}
		return s.enter(*a, price, now)
	}
	return nil
}

// ladderStop is the stop the caller's targets allow for p.Best: T1 → our
// entry, T[i] → T[i-1], past the last target → also LadderTrailPct below
// the best. A call without targets steps to entry at +LadderTrailPct and
// trails from there. 0 when no rung is reached.
func (s *TelegramSignal) ladderStop(p *TGPosition) (int64, string) {
	trail := s.cfg.LadderTrailPct
	if trail <= 0 {
		return 0, ""
	}
	best := p.Best
	targets := p.Targets
	if len(targets) == 0 {
		targets = []int64{p.Entry * (100 + trail) / 100}
	}
	var stop int64
	rung := ""
	for i, t := range targets {
		if best < t {
			break
		}
		if i == 0 {
			stop, rung = p.Entry, fmt.Sprintf("T1 %d reached: cost", t)
		} else {
			stop, rung = targets[i-1], fmt.Sprintf("T%d %d reached: stop to T%d", i+1, t, i)
		}
	}
	if best >= targets[len(targets)-1] {
		if t := best * (100 - trail) / 100; t > stop {
			stop, rung = t, fmt.Sprintf("past last target, trail %d%% below best %d", trail, best)
		}
	}
	return stop, rung
}

// gapPct is |price-ref| as a % of ref.
func gapPct(price, ref int64) int64 {
	d := price - ref
	if d < 0 {
		d = -d
	}
	return d * 100 / ref
}

// expire drops calls that waited longer than ArmFor.
func (s *TelegramSignal) expire(now time.Time) {
	kept := s.arms[:0]
	for _, a := range s.arms {
		if now.Sub(a.ArmedAt) > s.cfg.ArmFor {
			s.drop(a, fmt.Sprintf("expired: no entry %d within %s", a.Call.TriggerPaise, s.cfg.ArmFor))
			continue
		}
		kept = append(kept, a)
	}
	s.arms = kept
}

func (s *TelegramSignal) enter(a TGArm, price int64, now time.Time) *Signal {
	entry := price
	if s.cfg.EntryCapPaise > 0 {
		entry = a.Call.TriggerPaise // the exchange order's price; the fill corrects it (FillEntry)
	}
	p := &TGPosition{Arm: a, Entry: entry, SL: a.Call.SLPaise, Last: price, Best: price,
		Targets: append([]int64(nil), a.Call.Targets...), EnteredTS: now, Resting: s.cfg.EntryCapPaise > 0,
		Lots: s.cfg.Lots, LotsLeft: s.cfg.Lots}
	switch {
	case s.cfg.CallTargets && len(p.Targets) > 0:
		p.Target = p.Targets[0] // the last one once T1's lots are sold
	case s.cfg.TargetPct > 0:
		p.Target = entry * (100 + s.cfg.TargetPct) / 100
	}
	s.pos, s.arms = p, nil
	c := a.Call
	s.event(c.Key(), "ENTERED", fmt.Sprintf("%s premium=%d sl=%d target=%d targets=%v lots=%d", a.FNOSymbol, price, p.SL, p.Target, p.Targets, p.Lots))
	sig := &Signal{
		StrategyName: s.Name(),
		Action:       ActionBuy,
		Side:         c.Side(),
		Token:        a.IndexToken,
		Exchange:     a.IndexExchange,
		Qty:          s.qtyOf(a),
		Price:        price,
		FNOToken:     a.FNOToken,
		FNOSymbol:    a.FNOSymbol,
		Strike:       c.Strike,
		Lots:         s.cfg.Lots,
		Reason: fmt.Sprintf("TG ENTRY %s %d%s trigger=%d premium=%d sl=%d target=%d msg=%s src=%s",
			c.Underlying, c.Strike, c.OptionType, c.TriggerPaise, price, p.SL, p.Target, c.Key(), c.Source),
		SignalTS: now,
	}
	if cap := s.cfg.EntryCapPaise; cap > 0 {
		// On Active: a LIMIT at the cap, marketable now. On price: the
		// trigger too, so the exchange fires it if the premium dipped back.
		sig.EntryLimitPaise = c.TriggerPaise + cap
		if !s.cfg.WaitActive {
			sig.EntryTriggerPaise = c.TriggerPaise
		}
		sig.EntryExpiresAt = a.ArmedAt.Add(s.cfg.ArmFor)
	}
	return sig
}

// partialDue: price reached the first target and part of the lots is
// still to be sold there. Needs a later target for the rest.
func (s *TelegramSignal) partialDue(p *TGPosition, price int64) bool {
	n := s.cfg.T1ExitLots
	return s.cfg.CallTargets && !p.T1Done && n > 0 && p.LotsLeft > n && len(p.Targets) > 1 && price >= p.Targets[0]
}

// partialExit sells T1ExitLots at the first target and keeps the rest
// open for the last target. The caller holds s.mu.
func (s *TelegramSignal) partialExit(p *TGPosition, price int64) *Signal {
	n := s.cfg.T1ExitLots
	p.T1Done = true
	p.LotsLeft -= n
	last := p.Targets[len(p.Targets)-1]
	if p.Target == p.Targets[0] { // not changed by hand
		p.Target = last
	}
	reason := fmt.Sprintf("TG T1 premium=%d t1=%d entry=%d: sell %d of %d lots, rest to %d", price, p.Targets[0], p.Entry, n, p.Lots, p.Target)
	s.event(p.Arm.Call.Key(), TGPartialExit, reason)
	sig := s.exitSignal(p, reason)
	sig.ExitLots = n
	return sig
}

// exit closes s.pos; the caller holds s.mu.
func (s *TelegramSignal) exit(reason string) *Signal {
	p := s.pos
	s.pos = nil
	return s.exitSignal(p, reason)
}

// exitSignal is the EXIT for p (all lots held unless ExitLots is set).
func (s *TelegramSignal) exitSignal(p *TGPosition, reason string) *Signal {
	return &Signal{
		StrategyName:  s.Name(),
		Action:        ActionExit,
		Side:          p.Arm.Call.Side(),
		Token:         p.Arm.IndexToken,
		Exchange:      p.Arm.IndexExchange,
		Qty:           s.qtyOf(p.Arm),
		EntryFNOPrice: p.Entry,
		FNOToken:      p.Arm.FNOToken,
		FNOSymbol:     p.Arm.FNOSymbol,
		Reason:        reason,
	}
}
