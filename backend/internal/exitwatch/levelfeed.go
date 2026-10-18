package exitwatch

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Wire shapes of the two external level feeds, decoded locally so exitwatch
// does not import the analyst or strategy packages for them.

// analyst LevelUpdateEvent (internal/analyst/model.go).
type analystLevelsMsg struct {
	Token  string `json:"token"`
	Levels []struct {
		Price     int64     `json:"price"`
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"levels"`
}

// NIFTY50_SR SRView (internal/strategy/sr_view.go); Key is "EXCH:TOKEN".
type ourSRMsg struct {
	Key    string  `json:"key"`
	ADX    float64 `json:"adx"` // 15m ADX
	Levels []struct {
		Price   int64  `json:"price"`
		Touches int    `json:"touches"`
		Source  string `json:"source"`
	} `json:"levels"`
}

// ourSRSet is one index's SR levels and 15m ADX.
type ourSRSet struct {
	levels []Level
	adx    float64
}

// parseAnalystLevels returns the index token and its levels.
func parseAnalystLevels(b []byte) (string, []Level, error) {
	var m analystLevelsMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return "", nil, fmt.Errorf("analyst levels: %w", err)
	}
	if m.Token == "" {
		return "", nil, fmt.Errorf("analyst levels: no token")
	}
	ls := make([]Level, 0, len(m.Levels))
	for _, l := range m.Levels {
		if l.Price > 0 {
			ls = append(ls, Level{Price: l.Price, Type: analystPrefix + l.Type, Since: l.CreatedAt})
		}
	}
	return m.Token, ls, nil
}

// parseOurSRXLevels splits a pub:srx map {"BSE:99919000": SRView} into
// one level set per index token.
func parseOurSRXLevels(b []byte) (map[string]ourSRSet, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("srx views: %w", err)
	}
	out := make(map[string]ourSRSet, len(m))
	for _, raw := range m {
		tok, ls, adx, err := parseOurSRLevels(raw)
		if err != nil {
			return nil, err
		}
		out[tok] = ourSRSet{levels: ls, adx: adx}
	}
	return out, nil
}

// parseOurSRLevels returns the index token, our SR strategy's levels and
// its 15m ADX. First-seen time is left to the book, so a level the strategy
// adds mid-run counts as new.
func parseOurSRLevels(b []byte) (string, []Level, float64, error) {
	var m ourSRMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return "", nil, 0, fmt.Errorf("sr view: %w", err)
	}
	tok := m.Key[strings.LastIndexByte(m.Key, ':')+1:]
	if tok == "" {
		return "", nil, 0, fmt.Errorf("sr view: no key")
	}
	ls := make([]Level, 0, len(m.Levels))
	for _, l := range m.Levels {
		if l.Price > 0 {
			ls = append(ls, OurSRLevel(l.Price, l.Source, l.Touches))
		}
	}
	return tok, ls, m.ADX, nil
}
