package strategy

import (
	"strings"

	"trading-systemv1/internal/model"
)

// PositionContexter is implemented by strategies that can describe their
// open option positions (entry, target, stop) to exitwatch.
type PositionContexter interface {
	PositionContexts() []model.PositionContext
}

// PositionContexts reports the open NIFTY50_SR positions.
func (s *Nifty50SR) PositionContexts() []model.PositionContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.PositionContext
	for key, st := range s.instruments {
		if st.Side == SideNone || st.TargetLevel == 0 {
			continue
		}
		out = append(out, model.PositionContext{
			Strategy: s.Name(), Side: string(st.Side), IndexToken: bareToken(key),
			IndexEntry: st.IndexEntry, TargetLevel: st.TargetLevel, StopLevel: st.StopLevel,
			FNOToken: bareToken(s.heldToken(st)), FNOEntryPrice: st.FNOEntryPrice,
		})
	}
	return out
}

// PositionContexts reports the open NIFTY50_RANGE positions.
func (s *Nifty50Range) PositionContexts() []model.PositionContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.PositionContext
	for key, st := range s.instruments {
		if st.Side == SideNone || st.TargetLevel == 0 {
			continue
		}
		token := st.FNOToken
		if token == "" {
			token = s.cfg.FNOPutToken
			if st.Side == SideCall {
				token = s.cfg.FNOCallToken
			}
		}
		out = append(out, model.PositionContext{
			Strategy: s.Name(), Side: string(st.Side), IndexToken: bareToken(key),
			IndexEntry: st.IndexEntry, TargetLevel: st.TargetLevel, StopLevel: st.StopLevel,
			FNOToken: bareToken(token), FNOEntryPrice: st.FNOEntryPrice,
		})
	}
	return out
}

// bareToken strips an "EXCH:" prefix.
func bareToken(t string) string {
	if i := strings.LastIndexByte(t, ':'); i >= 0 {
		return t[i+1:]
	}
	return t
}
