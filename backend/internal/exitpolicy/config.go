package exitpolicy

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"

	"trading-systemv1/internal/model"
)

// ThetaConfig tunes ThetaRule. Percentages are whole percent.
type ThetaConfig struct {
	MaxFlatMin          int   `json:"max_flat_min"`           // N: exit a flat position after this long
	DecayPctOfGain      int64 `json:"decay_pct_of_gain"`      // X: exit once decay ≥ X% of expected gain
	FlatProgressPct     int64 `json:"flat_progress_pct"`      // flat while best gain < this % of expected gain
	FallbackFlatPremPct int64 `json:"fallback_flat_prem_pct"` // without greeks: flat while best gain < this % of entry premium
	ConfirmMin          int   `json:"confirm_min"`            // consecutive 1m evaluations before acting
}

// StrategyConfig is one strategy's exit policy.
type StrategyConfig struct {
	Mode  Mode        `json:"mode"`
	Theta ThetaConfig `json:"theta"`
}

// Config is backend/config/exitpolicy.json.
type Config struct {
	Strategies map[string]StrategyConfig `json:"strategies"`
}

// DefaultThetaConfig: decay budget as the option picker's
// STRAT_SR_THETA_MAX_GAIN_PCT (25). MaxFlatMin 75 scored best on the SR
// backtest (Mar–Oct 2026; 60–150 all beat no theta exit on both halves).
func DefaultThetaConfig() ThetaConfig {
	return ThetaConfig{MaxFlatMin: 75, DecayPctOfGain: 25, FlatProgressPct: 30, FallbackFlatPremPct: 10, ConfirmMin: 2}
}

// DefaultConfig: SR acts (paper only).
func DefaultConfig() Config {
	return Config{Strategies: map[string]StrategyConfig{
		"NIFTY50_SR": {Mode: ModeAct, Theta: DefaultThetaConfig()},
	}}
}

// LoadConfig reads path over the defaults. A strategy in the file keeps
// default values for fields it omits; strategies not in the file keep
// their default entry.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	var raw struct {
		Strategies map[string]json.RawMessage `json:"strategies"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	out := Config{Strategies: maps.Clone(c.Strategies)}
	for name, msg := range raw.Strategies {
		sc, ok := out.Strategies[name]
		if !ok {
			sc = StrategyConfig{Mode: ModeOff, Theta: DefaultThetaConfig()}
		}
		if err := json.Unmarshal(msg, &sc); err != nil {
			return c, fmt.Errorf("parse %s %s: %w", path, name, err)
		}
		out.Strategies[name] = sc
	}
	if err := out.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// Validate rejects unknown modes, unusable theta settings and any attempt
// to let the policy close the real-order strategy.
func (c Config) Validate() error {
	for name, sc := range c.Strategies {
		if !sc.Mode.valid() {
			return fmt.Errorf("%s: unknown mode %q", name, sc.Mode)
		}
		if name == model.RealOrderStrategy && sc.Mode == ModeAct {
			return fmt.Errorf("%s places real orders; exit policy may not act on it", name)
		}
		t := sc.Theta
		if t.MaxFlatMin <= 0 || t.ConfirmMin <= 0 || t.DecayPctOfGain <= 0 || t.FlatProgressPct <= 0 || t.FallbackFlatPremPct <= 0 {
			return fmt.Errorf("%s: theta settings must all be > 0: %+v", name, t)
		}
	}
	return nil
}

// PlanFor builds the plan for a new position of strategy. ok is false when
// the strategy is unlisted or off. The real-order strategy never acts.
func (c Config) PlanFor(strategy string) (Plan, bool) {
	sc, ok := c.Strategies[strategy]
	if !ok || sc.Mode == ModeOff {
		return Plan{}, false
	}
	plan := sc.Plan()
	if strategy == model.RealOrderStrategy {
		plan.Mode = ModeShadow
	}
	return plan, true
}

// Plan is the plan this config gives a new position.
func (sc StrategyConfig) Plan() Plan {
	return Plan{Mode: sc.Mode, Rules: []Rule{ThetaRule{P: sc.Theta}}}
}
