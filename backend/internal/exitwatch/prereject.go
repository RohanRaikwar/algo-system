package exitwatch

import (
	"fmt"
	"time"
)

// ReasonPreReject is the EXIT reason of a rejection at a strong level
// before target (see preReject).
const ReasonPreReject = "SR_REJECT_PRE"

// preRejectIn is one evaluation of the pre-target rejection rule. Moves are
// favourable index moves from entry in paise; prices are index paise.
type preRejectIn struct {
	levels     []Level
	price      int64 // index LTP
	move       int64 // favourable move at price
	entry      int64 // index entry
	sign       int64 // +1 CALL, -1 PUT
	targetMove int64
	tol        int64   // "at the level" tolerance
	rej        int64   // pullback from the peak that counts as rejection
	breakN     int64   // ns beyond the level that count as a break
	adx        float64 // our SR strategy's 15m ADX, 0 = unknown

	// Latest closed 1m index bar: its close and start time (zero when none).
	close   int64
	closeTS time.Time

	now time.Time
	m   *Model
}

func (in *preRejectIn) moveOf(price int64) int64 { return (price - in.entry) * in.sign }

// preReject watches the nearest strong level (5m/1h swing or our SR level)
// between entry and a far target. A touch of it followed by a pullback of
// rej, confirmed by a 1m close back on our side, is a rejection: the target
// is unlikely and the trade should be closed. A level held beyond for
// breakN is broken and the next strong level ahead is watched.
// Pure state, so the backtest can drive it without a Tracker.
type preReject struct {
	level       Level
	has         bool
	touched     bool
	touchNs     int64 // unix ns of the first touch
	peak        int64 // best move since the touch
	beyondSince int64 // unix ns price first held beyond the level; 0 = not beyond
}

// pick returns the nearest strong level ahead of (or within tol of) price,
// past entry by tol, that leaves the target at least MinRoomPct of the
// target move away.
func (r *preReject) pick(in *preRejectIn) (Level, bool) {
	room := int64(in.m.PreRejectMinRoomPct * float64(in.targetMove))
	try := func(l Level) (bool, bool) { // (take, stop scanning)
		if !l.Strong || !l.qualifies(in.m.PreRejectSources, in.m.PreRejectMinTouches) {
			return false, false
		}
		lm := in.moveOf(l.Price)
		if lm < in.move-in.tol || lm < in.tol { // behind price, or at entry
			return false, false
		}
		if in.targetMove-lm < room {
			return false, true // this and every level further out is too near target
		}
		return true, true
	}
	if in.sign > 0 {
		for _, l := range in.levels {
			if take, stop := try(l); take {
				return l, true
			} else if stop {
				break
			}
		}
		return Level{}, false
	}
	for i := len(in.levels) - 1; i >= 0; i-- {
		if take, stop := try(in.levels[i]); take {
			return in.levels[i], true
		} else if stop {
			break
		}
	}
	return Level{}, false
}

// step advances the rule; exit is true once, with the detail text.
func (r *preReject) step(in preRejectIn) (exit bool, text string) {
	// Strong trend: levels tend to break, so neither watch nor fire. A touch
	// already seen is kept for when the trend eases.
	if in.m.PreRejectMaxADX > 0 && in.adx >= in.m.PreRejectMaxADX {
		return false, ""
	}
	nowNs := in.now.UnixNano()
	if !r.touched {
		r.level, r.has = r.pick(&in)
		if !r.has {
			return false, ""
		}
	}
	lm := in.moveOf(r.level.Price)
	dist := lm - in.move // > 0: level still ahead

	if !r.touched {
		if dist > in.tol || in.move < in.m.PreRejectMinMovePaise {
			return false, ""
		}
		r.touched, r.touchNs, r.peak, r.beyondSince = true, nowNs, in.move, 0
	}
	if in.move > r.peak {
		r.peak = in.move
	}

	// Break: held beyond the level, watch the next one.
	if dist < -in.tol {
		if r.beyondSince == 0 {
			r.beyondSince = nowNs
		}
		if nowNs-r.beyondSince >= in.breakN {
			r.touched, r.has, r.beyondSince = false, false, 0
		}
		return false, ""
	}
	r.beyondSince = 0

	if r.peak-in.move < in.rej {
		return false, ""
	}
	if in.m.PreRejectConfirmClose {
		// The bar must end after the touch and close back below the level.
		if in.closeTS.IsZero() || in.closeTS.Add(time.Minute).UnixNano() <= r.touchNs ||
			in.moveOf(in.close) >= lm-in.tol {
			return false, ""
		}
	}
	word := "resistance"
	if in.sign < 0 {
		word = "support"
	}
	tags := r.level.Tags
	if tags == "" {
		tags = r.level.Type
	}
	target := in.entry + in.sign*in.targetMove
	text = fmt.Sprintf("rejected at %s %s (%s) before target %s, pulled back %s pts",
		word, pts(r.level.Price), tags, pts(target), pts(r.peak-in.move))
	if r.level.Touches > 0 {
		text += fmt.Sprintf(" touches=%d", r.level.Touches)
	}
	if in.adx > 0 {
		text += fmt.Sprintf(" adx=%.1f", in.adx)
	}
	return true, text
}
