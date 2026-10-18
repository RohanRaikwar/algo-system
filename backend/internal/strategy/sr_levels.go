package strategy

import (
	"fmt"
	"log"
	"strings"
)

// LevelEdit is a hand edit of an open position's exits (dashboard). A zero
// field is left unchanged; FNOTarget -1 removes the premium target.
type LevelEdit struct {
	IndexStop   int64 // index paise
	IndexTarget int64
	FNOSL       int64 // option premium paise
	FNOTarget   int64
}

// SetLevels applies a hand edit to the open position on side. fnoToken,
// when both sides know it, must be the contract held. Index levels are
// checked against the last 1m close and premium levels against the last
// option LTP: a stop must sit on the losing side and a target on the
// winning side, or the edit is refused whole.
func (s *Nifty50SR) SetLevels(side PositionSide, fnoToken string, e LevelEdit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != side || side == SideNone {
			continue
		}
		if fnoToken != "" && st.FNOToken != "" && st.FNOToken != fnoToken && !strings.HasSuffix(st.FNOToken, ":"+fnoToken) {
			return fmt.Errorf("position holds %s, not %s", st.FNOToken, fnoToken)
		}
		if err := checkLevelEdit(st, e); err != nil {
			return err
		}
		if e.IndexStop > 0 || e.IndexTarget > 0 {
			if e.IndexStop > 0 {
				st.StopLevel = e.IndexStop
			}
			if e.IndexTarget > 0 {
				st.TargetLevel = e.IndexTarget
			}
			st.ManualLevels, st.SupportLvl, st.BreakBars = true, 0, 0
		}
		if e.FNOSL > 0 {
			st.FNOManualSL = e.FNOSL
		}
		switch {
		case e.FNOTarget > 0:
			st.FNOTarget = e.FNOTarget
		case e.FNOTarget < 0:
			st.FNOTarget = 0
		}
		log.Printf("[strategy] %s: %s levels set by hand — index stop=%d target=%d premium sl=%d target=%d",
			s.Name(), side, st.StopLevel, st.TargetLevel, st.FNOManualSL, st.FNOTarget)
		return nil
	}
	return fmt.Errorf("no open %s position", side)
}

func checkLevelEdit(st *nifty50SRState, e LevelEdit) error {
	sign := int64(1)
	if st.Side == SidePut {
		sign = -1
	}
	if e.IndexStop > 0 || e.IndexTarget > 0 {
		close := st.LastClose
		if close <= 0 {
			return fmt.Errorf("no index close yet")
		}
		if e.IndexStop > 0 && (close-e.IndexStop)*sign <= 0 {
			return fmt.Errorf("index stop %d is not on the losing side of %d", e.IndexStop, close)
		}
		if e.IndexTarget > 0 && (e.IndexTarget-close)*sign <= 0 {
			return fmt.Errorf("index target %d is not on the winning side of %d", e.IndexTarget, close)
		}
	}
	if e.FNOSL > 0 || e.FNOTarget > 0 {
		ltp := st.FNOLastPrice
		if ltp <= 0 {
			return fmt.Errorf("no option price yet")
		}
		if e.FNOSL > 0 && e.FNOSL >= ltp {
			return fmt.Errorf("premium stop %d is not below LTP %d", e.FNOSL, ltp)
		}
		if e.FNOTarget > 0 && e.FNOTarget <= ltp {
			return fmt.Errorf("premium target %d is not above LTP %d", e.FNOTarget, ltp)
		}
	}
	return nil
}
