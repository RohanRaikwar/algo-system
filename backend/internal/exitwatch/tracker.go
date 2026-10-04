package exitwatch

import (
	"fmt"
	"time"

	"trading-systemv1/internal/model"
)

// Position is the open-position context published by stratengine.
type Position = model.PositionContext

// Assessment is one evaluation result for a position.
type Assessment struct {
	Position
	P             float64
	Decision      Decision
	ExitReason    string // PROTECT or SCORE when Decision is EXIT
	Latched       bool
	Reasons       []string // top contributing features
	SuggestedStop int64    // index paise, set on TIGHTEN
	IndexLTP      int64
	PremiumLTP    int64
	TS            time.Time
	Snapshot      Snapshot
}

const (
	barRing = 128  // 1s micro-bars kept (~2 minutes)
	dirRing = 1024 // index tick directions kept

	velFloor       = 20.0 // paise/s; avoids dividing by a flat 60s velocity
	stallScaleSec  = 60.0
	imbalanceSec   = 30
	minImbTicks    = 5
	lowerHighBlock = 5 // seconds per micro-swing block
	lowerHighMax   = 4.0
	minWickRange   = 500 // paise; wick on a tiny bar is noise
	premDivScale   = 0.15
	spreadScale    = 1.0
	oiScale        = 0.20
	pullbackATRMax = 1.0
	timeWindowMin  = 30.0
	minPeakForGB   = 0.10 // giveback counts once peak >= 10% of target move
)

type microBar struct {
	sec           int64
	hi, lo, close int64 // favourable move, paise
}

type tickDir struct {
	ts  int64
	dir int8
}

// Tracker holds per-position tick state. It is not safe for concurrent use;
// the service drives each tracker from a single goroutine.
type Tracker struct {
	pos        Position
	m          *Model
	sign       int64
	targetMove int64

	haveIdx  bool
	idxLTP   int64
	lastMove int64
	peakMove int64
	peakTS   int64

	bars     [barRing]microBar
	barHead  int // index of latest bar
	barCount int

	dirs     [dirRing]tickDir
	dirHead  int
	dirCount int

	minute                  int64
	mOpen, mHi, mLo, mClose int64
	prem, peakPrem          int64
	spread                  int64
	spreadAvg               float64
	oi, oiFirst             int64
	atr                     int64
	dec                     decider
}

// NewTracker validates the position and returns a tracker for it.
func NewTracker(pos Position, m Model) (*Tracker, error) {
	var sign int64
	switch pos.Side {
	case "CALL":
		sign = 1
	case "PUT":
		sign = -1
	default:
		return nil, fmt.Errorf("exitwatch: bad side %q", pos.Side)
	}
	tm := (pos.TargetLevel - pos.IndexEntry) * sign
	if tm <= 0 {
		return nil, fmt.Errorf("exitwatch: target %d not in favour of %s entry %d", pos.TargetLevel, pos.Side, pos.IndexEntry)
	}
	return &Tracker{pos: pos, m: &m, sign: sign, targetMove: tm}, nil
}

// Position returns the tracked position context.
func (t *Tracker) Position() Position { return t.pos }

// SetModel swaps tuning after a reload. Latch state is kept.
func (t *Tracker) SetModel(m Model) { t.m = &m }

// SetATR sets the 1m ATR in paise used to normalise pullbacks.
func (t *Tracker) SetATR(atr int64) { t.atr = atr }

// OnIndexTick feeds one index tick. ts must be the tick's canonical time.
func (t *Tracker) OnIndexTick(price int64, ts time.Time) {
	now := ts.UnixNano()
	mv := (price - t.pos.IndexEntry) * t.sign

	if t.haveIdx && mv != t.lastMove {
		var d int8 = 1
		if mv < t.lastMove {
			d = -1
		}
		t.dirHead = (t.dirHead + 1) % dirRing
		t.dirs[t.dirHead] = tickDir{ts: now, dir: d}
		if t.dirCount < dirRing {
			t.dirCount++
		}
	}
	if !t.haveIdx || mv > t.peakMove {
		t.peakMove, t.peakTS = mv, now
	}
	t.haveIdx, t.idxLTP, t.lastMove = true, price, mv

	sec := now / int64(time.Second)
	if t.barCount > 0 && t.bars[t.barHead].sec == sec {
		b := &t.bars[t.barHead]
		if mv > b.hi {
			b.hi = mv
		}
		if mv < b.lo {
			b.lo = mv
		}
		b.close = mv
	} else {
		t.barHead = (t.barHead + 1) % barRing
		t.bars[t.barHead] = microBar{sec: sec, hi: mv, lo: mv, close: mv}
		if t.barCount < barRing {
			t.barCount++
		}
	}

	min := now / int64(time.Minute)
	if min != t.minute {
		t.minute, t.mOpen, t.mHi, t.mLo = min, mv, mv, mv
	}
	if mv > t.mHi {
		t.mHi = mv
	}
	if mv < t.mLo {
		t.mLo = mv
	}
	t.mClose = mv
}

// OnOptionTick feeds one tick of the held option. bid, ask and oi are 0 when
// the feed is not in SnapQuote mode.
func (t *Tracker) OnOptionTick(price, bid, ask, oi int64, _ time.Time) {
	if price > 0 {
		t.prem = price
		if price > t.peakPrem {
			t.peakPrem = price
		}
	}
	if bid > 0 && ask > bid {
		t.spread = ask - bid
		if t.spreadAvg == 0 {
			t.spreadAvg = float64(t.spread)
		} else {
			t.spreadAvg += 0.05 * (float64(t.spread) - t.spreadAvg)
		}
	}
	if oi > 0 {
		t.oi = oi
		if t.oiFirst == 0 {
			t.oiFirst = oi
		}
	}
}

// Evaluate computes features, score and decision at now.
func (t *Tracker) Evaluate(now time.Time) Assessment {
	a := Assessment{Position: t.pos, IndexLTP: t.idxLTP, PremiumLTP: t.prem, TS: now}
	if !t.haveIdx {
		a.Decision = DecisionHold
		return a
	}
	s := t.features(now)
	p, top, n := score(t.m, &s)

	premOK := true
	if t.prem > 0 && t.pos.FNOEntryPrice > 0 {
		floor := float64(t.pos.FNOEntryPrice) * (1 - t.m.PremiumBufferPct)
		premOK = float64(t.prem) >= floor
	} else {
		premOK = t.lastMove > 0
	}
	dec, why := t.dec.step(t.m, &s, p, premOK, now.UnixNano())

	a.P, a.Decision, a.ExitReason, a.Latched, a.Snapshot = p, dec, why, t.dec.latched, s
	a.Reasons = make([]string, n)
	for i := 0; i < n; i++ {
		a.Reasons[i] = top[i].String()
	}
	if dec == DecisionTighten && t.peakMove > 0 {
		keep := int64(float64(t.peakMove) * t.m.TightenKeepPct)
		a.SuggestedStop = t.pos.IndexEntry + t.sign*keep
	}
	return a
}

func (t *Tracker) features(now time.Time) Snapshot {
	var s Snapshot
	nowNs := now.UnixNano()
	tm := float64(t.targetMove)
	s.Progress = float64(t.lastMove) / tm
	s.PeakProgress = float64(t.peakMove) / tm

	set := func(f Feature, raw, z float64) {
		s.Raw[f], s.Z[f], s.Present[f] = raw, clip01(z), true
	}

	if s.PeakProgress >= minPeakForGB {
		s.Giveback = float64(t.peakMove-t.lastMove) / float64(t.peakMove)
		set(FGiveback, s.Giveback, s.Giveback)
	} else {
		set(FGiveback, 0, 0)
	}

	stall := float64(nowNs-t.peakTS) / 1e9
	set(FStall, stall, stall/stallScaleSec)

	if v10, ok := t.velocity(10); ok {
		v60, _ := t.velocity(60)
		abs := v60
		if abs < 0 {
			abs = -abs
		}
		r := v10 / (abs + velFloor)
		set(FVelocity, r, (1-r)/2)
	}

	if imb, ok := t.imbalance(nowNs); ok {
		set(FImbalance, imb, -imb)
	}

	if t.barCount >= 2*lowerHighBlock {
		lh := float64(t.lowerHighs())
		set(FLowerHighs, lh, lh/lowerHighMax)
	}

	if rng := t.mHi - t.mLo; rng > 0 {
		wick := float64(t.mHi-t.mClose) / float64(rng)
		minRange := int64(minWickRange)
		if t.atr/4 > minRange {
			minRange = t.atr / 4
		}
		weight := clip01(float64(rng) / float64(minRange))
		set(FWick, wick, wick*weight)
	}

	if t.prem > 0 && t.peakPrem > 0 {
		dd := float64(t.peakPrem-t.prem) / float64(t.peakPrem)
		div := 0.0
		if s.Giveback < 0.25 {
			div = dd
		}
		set(FPremDiv, div, div/premDivScale)
	}

	if t.spread > 0 && t.spreadAvg > 0 {
		r := float64(t.spread)/t.spreadAvg - 1
		set(FSpread, r, r/spreadScale)
	}

	if t.oi > 0 && t.oiFirst > 0 {
		chg := float64(t.oi-t.oiFirst) / float64(t.oiFirst)
		set(FOIChange, chg, chg/oiScale)
	}

	if t.atr > 0 && t.peakMove > 0 {
		pb := float64(t.peakMove-t.lastMove) / float64(t.atr)
		set(FPullbackATR, pb, pb/pullbackATRMax)
	}

	if t.m.TimeExitMin > 0 {
		ist := now.In(istZone)
		left := float64(t.m.TimeExitMin - (ist.Hour()*60 + ist.Minute()))
		set(FTimePressure, left, 1-left/timeWindowMin)
	}
	return s
}

var istZone = time.FixedZone("IST", 5*3600+30*60)

// velocity returns favourable move per second over the last window seconds.
func (t *Tracker) velocity(window int64) (float64, bool) {
	if t.barCount < 2 {
		return 0, false
	}
	latest := t.bars[t.barHead]
	target := latest.sec - window
	var ref microBar
	found := false
	for i := 1; i < t.barCount; i++ {
		b := t.bars[(t.barHead-i+barRing)%barRing]
		ref, found = b, true
		if b.sec <= target {
			break
		}
	}
	if !found || latest.sec == ref.sec {
		return 0, false
	}
	return float64(latest.close-ref.close) / float64(latest.sec-ref.sec), true
}

// imbalance returns (up - down)/total index tick moves in the last 30s.
func (t *Tracker) imbalance(now int64) (float64, bool) {
	cut := now - imbalanceSec*int64(time.Second)
	var up, total int
	for i := 0; i < t.dirCount; i++ {
		d := t.dirs[(t.dirHead-i+dirRing)%dirRing]
		if d.ts < cut {
			break
		}
		total++
		if d.dir > 0 {
			up++
		}
	}
	if total < minImbTicks {
		return 0, false
	}
	return float64(2*up-total) / float64(total), true
}

// lowerHighs counts consecutive falling 5s-block highs, newest first.
func (t *Tracker) lowerHighs() int {
	const blocks = 6
	var hi [blocks]int64
	var seen [blocks]bool
	latest := t.bars[t.barHead].sec
	for i := 0; i < t.barCount; i++ {
		b := t.bars[(t.barHead-i+barRing)%barRing]
		k := (latest - b.sec) / lowerHighBlock
		if k >= blocks {
			break
		}
		if !seen[k] || b.hi > hi[k] {
			hi[k], seen[k] = b.hi, true
		}
	}
	n := 0
	for k := 0; k+1 < blocks && seen[k] && seen[k+1]; k++ {
		if hi[k] >= hi[k+1] {
			break
		}
		n++
	}
	return n
}
