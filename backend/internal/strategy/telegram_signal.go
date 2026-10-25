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
// calls. It is paper unless STRAT_LIVE_STRATEGIES names it.
const TelegramSignalName = "TELEGRAM_SIGNAL"

// TelegramSignalConfig tunes TelegramSignal.
type TelegramSignalConfig struct {
	// TargetPct is our own premium target above the entry, % (0 = none).
	// The caller's targets are ignored.
	TargetPct int64
	// MaxChasePct refuses an entry whose premium is already more than this
	// % above the call's trigger.
	MaxChasePct int64
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
}

// TGArm is a call waiting for its trigger, with its contract resolved.
type TGArm struct {
	Call          model.TGSignal
	IndexToken    string
	IndexExchange string
	FNOExchange   string // NFO, BFO
	FNOToken      string // bare option token
	FNOSymbol     string
	ArmedAt       time.Time
	Seen          bool // a premium tick was checked against the trigger
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
}

// Arm results.
const (
	TGArmed       = "ARMED"
	TGReplaced    = "REPLACED"     // same message or contract was waiting
	TGSLRaised    = "SL_RAISED"    // edit of the held call raised its stop
	TGEditIgnored = "EDIT_IGNORED" // edit of the held call that would add risk
)

// TelegramSignal trades option calls from a Telegram group: it buys the
// named contract when its premium reaches the call's trigger, exits at the
// call's stop, at its own target, or when the caller's reply says so. One
// trade at a time.
type TelegramSignal struct {
	mu   sync.Mutex
	qty  int64
	cfg  TelegramSignalConfig
	arms []TGArm
	pos  *TGPosition

	// onEvent reports what happened to a call (callKey = TGSignal.Key).
	// Called with s.mu held: it must not block or call back.
	onEvent func(callKey, event, detail string)
}

// NewTelegramSignal builds the strategy.
func NewTelegramSignal(qty int64, cfg TelegramSignalConfig) *TelegramSignal {
	if cfg.ArmFor <= 0 {
		cfg.ArmFor = 30 * time.Minute
	}
	return &TelegramSignal{qty: qty, cfg: cfg}
}

// SetOnEvent sets the event callback (review log). It must not block.
func (s *TelegramSignal) SetOnEvent(f func(callKey, event, detail string)) {
	s.mu.Lock()
	s.onEvent = f
	s.mu.Unlock()
}

func (s *TelegramSignal) event(callKey, event, detail string) {
	log.Printf("[strategy] %s: call %s %s %s", s.Name(), callKey, event, detail)
	if s.onEvent != nil {
		s.onEvent(callKey, event, detail)
	}
}

func (s *TelegramSignal) Name() string { return TelegramSignalName }

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
	p.SL = sl
	s.event(p.Arm.Call.Key(), TGSLRaised, fmt.Sprintf("%s: stop %d → %d", why, old, sl))
	return TGSLRaised, nil
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
		s.event(a.Call.Key(), "DROPPED", reason)
		return nil, "DROPPED", nil
	}
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
		s.event(a.Call.Key(), "DROPPED", reason)
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
				p.SL = stop
				s.event(p.Arm.Call.Key(), TGSLRaised, fmt.Sprintf("%s → stop %d (was %d)", rung, stop, old))
			}
		}
		var reason string
		switch {
		case price <= p.SL && p.SL > p.Arm.Call.SLPaise:
			reason = fmt.Sprintf("TG RAISED STOP premium=%d stop=%d entry=%d best=%d", price, p.SL, p.Entry, p.Best)
		case price <= p.SL:
			reason = fmt.Sprintf("TG SL premium=%d sl=%d entry=%d", price, p.SL, p.Entry)
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
		drop := ""
		switch {
		case !a.Seen && s.cfg.MaxPriceGapPct > 0 && gapPct(price, c.TriggerPaise) > s.cfg.MaxPriceGapPct:
			drop = fmt.Sprintf("premium %d is more than %d%% from entry %d — call misread or wrong contract", price, s.cfg.MaxPriceGapPct, c.TriggerPaise)
		case price <= c.SLPaise:
			drop = fmt.Sprintf("premium %d hit stop %d before entry %d", price, c.SLPaise, c.TriggerPaise)
		case price < c.TriggerPaise:
			a.Seen = true
			return nil
		case price*100 > c.TriggerPaise*(100+s.cfg.MaxChasePct):
			drop = fmt.Sprintf("missed: premium %d more than %d%% above entry %d", price, s.cfg.MaxChasePct, c.TriggerPaise)
		}
		if drop != "" {
			s.event(c.Key(), "DROPPED", drop)
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
			s.event(a.Call.Key(), "DROPPED", fmt.Sprintf("expired: no entry %d within %s", a.Call.TriggerPaise, s.cfg.ArmFor))
			continue
		}
		kept = append(kept, a)
	}
	s.arms = kept
}

func (s *TelegramSignal) enter(a TGArm, price int64, now time.Time) *Signal {
	p := &TGPosition{Arm: a, Entry: price, SL: a.Call.SLPaise, Last: price, Best: price,
		Targets: append([]int64(nil), a.Call.Targets...), EnteredTS: now}
	if s.cfg.TargetPct > 0 {
		p.Target = price * (100 + s.cfg.TargetPct) / 100
	}
	s.pos, s.arms = p, nil
	c := a.Call
	s.event(c.Key(), "ENTERED", fmt.Sprintf("%s premium=%d sl=%d target=%d", a.FNOSymbol, price, p.SL, p.Target))
	return &Signal{
		StrategyName: s.Name(),
		Action:       ActionBuy,
		Side:         c.Side(),
		Token:        a.IndexToken,
		Exchange:     a.IndexExchange,
		Qty:          s.qty,
		Price:        price,
		FNOToken:     a.FNOToken,
		FNOSymbol:    a.FNOSymbol,
		Strike:       c.Strike,
		Reason: fmt.Sprintf("TG ENTRY %s %d%s trigger=%d premium=%d sl=%d target=%d msg=%s src=%s",
			c.Underlying, c.Strike, c.OptionType, c.TriggerPaise, price, p.SL, p.Target, c.Key(), c.Source),
		SignalTS: now,
	}
}

// exit closes s.pos; the caller holds s.mu.
func (s *TelegramSignal) exit(reason string) *Signal {
	p := s.pos
	s.pos = nil
	return &Signal{
		StrategyName:  s.Name(),
		Action:        ActionExit,
		Side:          p.Arm.Call.Side(),
		Token:         p.Arm.IndexToken,
		Exchange:      p.Arm.IndexExchange,
		Qty:           s.qty,
		EntryFNOPrice: p.Entry,
		FNOToken:      p.Arm.FNOToken,
		FNOSymbol:     p.Arm.FNOSymbol,
		Reason:        reason,
	}
}
