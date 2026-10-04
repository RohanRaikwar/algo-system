package exitwatch

import (
	"encoding/json"
	"fmt"
	"os"
)

// Feature indexes the reversal features. Every feature is side-normalised:
// "up" means in the position's favour, so a CALL and a PUT read the same way.
type Feature int

const (
	FGiveback     Feature = iota // share of peak move given back
	FStall                       // seconds since last new peak
	FVelocity                    // 10s velocity vs 60s velocity (decay / flip)
	FImbalance                   // up-ticks vs down-ticks, last 30s
	FLowerHighs                  // consecutive lower 5s highs
	FWick                        // forming 1m wick against us / bar range
	FPremDiv                     // premium falling while index holds near peak
	FSpread                      // option spread widening vs its average
	FOIChange                    // option OI change since first quote
	FPullbackATR                 // pullback from peak in ATR units
	FTimePressure                // closeness to time exit
	numFeatures
)

var featureNames = [numFeatures]string{
	"giveback", "stall", "velocity", "imbalance", "lower_highs", "wick",
	"prem_div", "spread", "oi_change", "pullback_atr", "time_pressure",
}

func (f Feature) String() string { return featureNames[f] }

// Params is the JSON-loadable tuning for the scorer and decision rules.
type Params struct {
	Bias    float64            `json:"bias"`
	Weights map[string]float64 `json:"weights"`

	ExitP               float64 `json:"exit_p"`                // score EXIT threshold
	TightenP            float64 `json:"tighten_p"`             // TIGHTEN advisory threshold
	ConfirmSec          float64 `json:"confirm_sec"`           // score must stay >= ExitP this long
	MinPeakProgress     float64 `json:"min_peak_progress"`     // score EXIT only after real move toward target
	ProtectPeakProgress float64 `json:"protect_peak_progress"` // protect EXIT: peak reached at least this
	ProtectGiveback     float64 `json:"protect_giveback"`      // protect EXIT: and gave back at least this
	PremiumBufferPct    float64 `json:"premium_buffer_pct"`    // score EXIT allowed down to entry*(1-buf)
	TightenKeepPct      float64 `json:"tighten_keep_pct"`      // suggested stop keeps this share of MFE
	TimeExitMin         int     `json:"time_exit_min"`         // IST minute of day, e.g. 910 = 15:10

	// Runner: target hit and momentum strong → hold to the next S/R.
	RunnerLockPct    float64 `json:"runner_lock_pct"`     // lock = entry + this × target move
	RunnerMinRoomPct float64 `json:"runner_min_room_pct"` // next level must be this × target move away to hold
	RejectATR        float64 `json:"reject_atr"`          // pullback from peak (× ATR) that counts as rejection
	RejectMinPaise   int64   `json:"reject_min_paise"`    // floor for the rejection pullback
	LevelTolATR      float64 `json:"level_tol_atr"`       // "at the level" tolerance (× ATR)
	LevelTolMinPaise int64   `json:"level_tol_min_paise"` // floor for the level tolerance
	BreakHoldSec     float64 `json:"break_hold_sec"`      // beyond level this long = broken, ladder up
	RunnerGiveback   float64 `json:"runner_giveback"`     // give back this share of the extension past target
	GhostMaxMin      int     `json:"ghost_max_min"`       // follow a closed runner at most this long
	GhostEndMin      int     `json:"ghost_end_min"`       // IST minute of day ghosts stop, e.g. 925 = 15:25

	// Day level book.
	FractalBars        int   `json:"fractal_bars"`          // 1m bars each side for a swing high/low
	ORBMin             int   `json:"orb_min"`               // opening range length in minutes from 09:15
	LevelMergeTolPaise int64 `json:"level_merge_tol_paise"` // levels this close merge into one
}

// DefaultParams returns hand-set weights. They are a starting point for
// shadow mode, to be tuned from the exitwatch report.
func DefaultParams() Params {
	return Params{
		Bias: -4.0,
		Weights: map[string]float64{
			"giveback":      2.5,
			"stall":         1.0,
			"velocity":      1.5,
			"imbalance":     1.0,
			"lower_highs":   0.8,
			"wick":          0.8,
			"prem_div":      1.2,
			"spread":        0.4,
			"oi_change":     0.3,
			"pullback_atr":  1.0,
			"time_pressure": 0.8,
		},
		ExitP:               0.70,
		TightenP:            0.50,
		ConfirmSec:          3,
		MinPeakProgress:     0.40,
		ProtectPeakProgress: 0.60,
		ProtectGiveback:     0.50,
		PremiumBufferPct:    0.05,
		TightenKeepPct:      0.30,
		TimeExitMin:         15*60 + 10,

		RunnerLockPct:    0.70,
		RunnerMinRoomPct: 0.30,
		RejectATR:        0.25,
		RejectMinPaise:   500,
		LevelTolATR:      0.10,
		LevelTolMinPaise: 300,
		BreakHoldSec:     30,
		RunnerGiveback:   0.50,
		GhostMaxMin:      60,
		GhostEndMin:      15*60 + 25,

		FractalBars:        2,
		ORBMin:             15,
		LevelMergeTolPaise: 500,
	}
}

// LoadParams reads Params from a JSON file. Missing fields keep defaults.
func LoadParams(path string) (Params, error) {
	p := DefaultParams()
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("parse %s: %w", path, err)
	}
	for name := range p.Weights {
		if featureIndex(name) < 0 {
			return p, fmt.Errorf("unknown feature weight %q", name)
		}
	}
	return p, nil
}

func featureIndex(name string) Feature {
	for i, n := range featureNames {
		if n == name {
			return Feature(i)
		}
	}
	return -1
}

// Model is the compiled, allocation-free form of Params used per tick.
type Model struct {
	Params
	W        [numFeatures]float64
	confirmN int64 // ConfirmSec in nanoseconds
	breakN   int64 // BreakHoldSec in nanoseconds
}

// Compile converts Params into a Model.
func (p Params) Compile() Model {
	m := Model{Params: p, confirmN: int64(p.ConfirmSec * 1e9), breakN: int64(p.BreakHoldSec * 1e9)}
	for name, w := range p.Weights {
		if f := featureIndex(name); f >= 0 {
			m.W[f] = w
		}
	}
	return m
}
