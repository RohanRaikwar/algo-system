package exitwatch

import "math"

// Decision is the exitwatch verdict for one open position.
type Decision string

const (
	DecisionHold    Decision = "HOLD"
	DecisionTighten Decision = "TIGHTEN"
	DecisionExit    Decision = "EXIT"
)

// Snapshot holds the computed features for one evaluation.
type Snapshot struct {
	Progress     float64 // current favourable move / target move
	PeakProgress float64 // best favourable move / target move
	Giveback     float64 // (peak - now) / peak, 0 when no meaningful peak
	Raw          [numFeatures]float64
	Z            [numFeatures]float64 // normalised to [0,1], 1 = strong reversal evidence
	Present      [numFeatures]bool
}

// minReasonContrib hides features that barely moved the score from Reasons.
const minReasonContrib = 0.25

// score returns the reversal probability and the top-3 contributing features.
// Missing features get weight 0 and the present weights are scaled up so the
// total weight mass stays the same.
func score(m *Model, s *Snapshot) (float64, [3]Feature, int) {
	var total, present float64
	for f := Feature(0); f < numFeatures; f++ {
		total += m.W[f]
		if s.Present[f] {
			present += m.W[f]
		}
	}
	scale := 1.0
	if present > 0 {
		scale = total / present
	}
	var contrib [numFeatures]float64
	sum := m.Bias
	for f := Feature(0); f < numFeatures; f++ {
		if s.Present[f] {
			contrib[f] = m.W[f] * s.Z[f] * scale
			sum += contrib[f]
		}
	}
	p := 1 / (1 + math.Exp(-sum))

	var top [3]Feature
	n := 0
	var used [numFeatures]bool
	for n < 3 {
		best, bestV := Feature(-1), minReasonContrib
		for f := Feature(0); f < numFeatures; f++ {
			if !used[f] && contrib[f] > bestV {
				best, bestV = f, contrib[f]
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		top[n] = best
		n++
	}
	return p, top, n
}

// decider turns scores into HOLD / TIGHTEN / EXIT with hysteresis.
// EXIT latches: once fired for a position it never reverts.
type decider struct {
	aboveSince int64 // unix ns when p first reached ExitP, 0 when below
	latched    bool
	reason     string
}

// step applies the decision rules. premOK reports whether the option premium
// is still at or near entry, so a score EXIT fires before the trade is a loss.
func (d *decider) step(m *Model, s *Snapshot, p float64, premOK bool, now int64) (Decision, string) {
	if d.latched {
		return DecisionExit, d.reason
	}
	if s.PeakProgress >= m.ProtectPeakProgress && s.Giveback >= m.ProtectGiveback {
		d.latched, d.reason = true, "PROTECT"
		return DecisionExit, d.reason
	}
	if p >= m.ExitP {
		if d.aboveSince == 0 {
			d.aboveSince = now
		}
		if now-d.aboveSince >= m.confirmN && s.PeakProgress >= m.MinPeakProgress && premOK {
			d.latched, d.reason = true, "SCORE"
			return DecisionExit, d.reason
		}
	} else {
		d.aboveSince = 0
	}
	if p >= m.TightenP {
		return DecisionTighten, ""
	}
	return DecisionHold, ""
}

func clip01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
