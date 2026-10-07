package exitpolicy

import "fmt"

// ThetaRule closes a flat position that time decay is eating. The option
// picker accepted the strike on the assumption that theta over the hold
// stays within a share of the expected gain; this rule enforces that
// assumption after entry.
//
// Decay is the larger of
//   - modeled:  |theta| per day × minutes held / 375
//   - realized: premium lost beyond what delta explains of the index move
//     (theta plus IV crush), floored at 0.
//
// The position is flat while its best premium gain is under
// FlatProgressPct of the expected gain (or, without greeks, under
// FallbackFlatPremPct of the entry premium) and the strategy has not armed
// a protective stop. A flat position exits after MaxFlatMin minutes, or as
// soon as decay reaches DecayPctOfGain of the expected gain.
type ThetaRule struct {
	P ThetaConfig
}

func (r ThetaRule) Name() Reason        { return ReasonTheta }
func (r ThetaRule) ConfirmMinutes() int { return r.P.ConfirmMin }

func (r ThetaRule) Evaluate(p Position, s Snapshot) (Decision, bool) {
	if p.PremEntry <= 0 || s.PremLTP <= 0 || s.ProtectArmed || p.EntryTS.IsZero() {
		return Decision{}, false
	}
	held := int64(s.Now.Sub(p.EntryTS).Minutes())
	bestGain := s.PremBest - p.PremEntry
	g := p.Greeks

	if g == nil {
		if bestGain*100 >= p.PremEntry*r.P.FallbackFlatPremPct || held < int64(r.P.MaxFlatMin) {
			return Decision{}, false
		}
		return r.decide(p, s, held, fmt.Sprintf("THETA %s flat %dm (no greeks) best=%s",
			p.Side, held, rupees(bestGain)), map[string]int64{"held_min": held, "best_gain": bestGain}), true
	}

	if bestGain*100 >= g.ExpGainPaise*r.P.FlatProgressPct {
		return Decision{}, false
	}
	modeled := g.DecayPerDayPaise * held / sessionMinutes
	var realized int64
	if s.IndexLTP > 0 && p.IndexEntry > 0 {
		expected := p.PremEntry + g.DeltaMilli*(s.IndexLTP-p.IndexEntry)/1000
		realized = max(0, expected-s.PremLTP)
	}
	decay, source := modeled, "modeled"
	if realized > modeled {
		decay, source = realized, "realized"
	}
	overBudget := decay*100 >= g.ExpGainPaise*r.P.DecayPctOfGain
	if !overBudget && held < int64(r.P.MaxFlatMin) {
		return Decision{}, false
	}
	text := fmt.Sprintf("THETA %s flat %dm decay=%s (%d%% of exp gain %s) %s",
		p.Side, held, rupees(decay), decay*100/g.ExpGainPaise, rupees(g.ExpGainPaise), source)
	return r.decide(p, s, held, text, map[string]int64{
		"held_min": held, "best_gain": bestGain, "modeled": modeled, "realized": realized,
		"decay": decay, "exp_gain": g.ExpGainPaise,
	}), true
}

func (r ThetaRule) decide(p Position, s Snapshot, held int64, text string, detail map[string]int64) Decision {
	detail["prem_ltp"] = s.PremLTP
	detail["index_ltp"] = s.IndexLTP
	return Decision{Position: p, Reason: ReasonTheta, Text: text, Detail: detail, TS: s.Now}
}
