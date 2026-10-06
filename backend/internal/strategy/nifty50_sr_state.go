package strategy

import (
	"encoding/json"
	"time"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 SR — regime-aware support/resistance strategy (paper).
//
//  The regime (sr_context.go) picks the setup:
//    RANGE       → FADE: rejection candle at a level, target the next level.
//    any but DEAD → BREAKOUT-RETEST: a close through a level arms the
//                  setup; entry only on a rejection at the retest.
//    TREND_UP/DN → PULLBACK: dip to VWAP / EMA21 / a level, entry on a
//                  candle closing back in the trend direction.
//  Every entry needs MinConfirmations of: VWAP side, EMA9/21, RSI, candle.
//  Stops sit beyond the level (ATR buffer); targets at the next level.
//  Daily caps: trades, consecutive losses, day loss in index points.
//  Index levels are int64 paise.
// ════════════════════════════════════════════════════════════════════

// Setup kinds recorded on a position.
const (
	SRKindFade     = "FADE"
	SRKindRetest   = "BREAKOUT_RETEST"
	SRKindPullback = "PULLBACK"
)

// Nifty50SRConfig holds all configurable parameters.
type Nifty50SRConfig struct {
	Context SRContextConfig

	FadeEnabled     bool
	RetestEnabled   bool
	PullbackEnabled bool

	MinConfirmations int // of 4: VWAP, EMA, RSI, candle

	// EntryTFMinutes is the bar setups are judged on: 1 (every 1m close)
	// or 5. Regime, VWAP slope, EMA9/21 and RSI stay on 5m bars either way.
	EntryTFMinutes int

	TouchTolPts      int64 // paise; a bar within this of a level "touches" it
	BreakATRPct      int64 // break close ≥ this % of 15m ATR beyond the level
	BreakVolPct      int64 // break bar volume ≥ this % of average (skipped without volume data)
	RetestMinutes    int   // minutes a break waits for its retest
	FlatEMAPts       int64 // |EMA9 − EMA21| ≤ this counts as flat (fade)
	FadeRSIBuyMax    float64
	FadeRSISellMin   float64
	PullRSILo        float64 // RSI reset band on a pullback in an uptrend…
	PullRSIHi        float64
	StopATRPct       int64 // stop beyond the level by this % of 15m ATR…
	StopMinPts       int64 // …but at least this (paise)
	DefaultTargetR   int64 // target in R (×100) when no next level exists
	MinRewardRiskPct int64 // index reward ≥ this % of risk (150 = 1.5:1)
	BreakevenAtR     int64 // move the stop to entry once gain ≥ this R (×100), 0 = off

	// No-progress exit: flat once NoProgressMin minutes have passed since
	// entry without any 1m close reaching NoProgressRPct of R in favour.
	// 0 = off.
	NoProgressMin  int
	NoProgressRPct int64

	// ReentryMinADX: a second same-side entry in a day needs 15m ADX at
	// least this, or higher than at the previous entry. 0 = off.
	ReentryMinADX float64

	// Day extreme: no CALL once price is at or above DayExtremePct % of
	// today's range after running at least DayRunPts (paise) up from the
	// day low. 0 = off. DayExtremePuts applies the mirror to PUTs (off:
	// it cut the PUT winners on falling days in the Mar–Oct 2026 backtest).
	DayExtremePct  int64
	DayRunPts      int64
	DayExtremePuts bool
	// DayExtremeFromMin: the day-extreme check starts at this IST minute
	// (early in the session any rally sits at "100% of range"), 0 = always.
	DayExtremeFromMin int

	// Sideways box: no entry while the last BoxBars 5m bars span at most
	// BoxATRPct % of the 15m ATR (price boxed in, whatever the regime
	// says). BoxFadeToo also refuses fades. 0 = off.
	BoxBars    int
	BoxATRPct  int64
	BoxFadeToo bool

	// GateMode for the day-extreme and sideways checks: "warn" takes the
	// entry and flags it (SR is paper-only; the user wants the signal),
	// "block" refuses it.
	GateMode string

	// RetestTolPts: how close a retest bar must come back to the broken
	// level (paise); 0 = TouchTolPts.
	RetestTolPts int64

	EntryFromMin    int // no entries before (IST minutes)
	EntryToMin      int // no entries from
	TimeExitMin     int // flat at
	MaxTradesPerDay int
	MaxConsecLosses int
	MaxDayLossPts   int64 // index paise lost today (sum of closed trades), 0 = off
	CooldownCandles int   // 1m candles after an exit

	FNOHardSLPct     int64
	FNOTrailStartPct int64
	FNOTrailSLPct    int64

	StrikeStep int64

	VolumeToken  string
	FNOCallToken string
	FNOPutToken  string
	IndexToken   string
	NameOverride string
}

// GateMode values.
const (
	SRGateWarn  = "warn"
	SRGateBlock = "block"
)

// DefaultNifty50SRConfig returns the default parameters.
func DefaultNifty50SRConfig() Nifty50SRConfig {
	return Nifty50SRConfig{
		Context: DefaultSRContextConfig(),

		FadeEnabled:     true,
		RetestEnabled:   true,
		PullbackEnabled: true,

		MinConfirmations: 3,
		EntryTFMinutes:   1,

		TouchTolPts:      1000, // 10 pts
		BreakATRPct:      25,
		BreakVolPct:      120,
		RetestMinutes:    30,
		FlatEMAPts:       1000,
		FadeRSIBuyMax:    40,
		FadeRSISellMin:   60,
		PullRSILo:        40,
		PullRSIHi:        55,
		StopATRPct:       100,  // 1× 15m ATR: 0.5× was stopped out by 5m noise (Mar-Sep 2026 backtest)
		StopMinPts:       2000, // 20 pts
		DefaultTargetR:   200,
		MinRewardRiskPct: 150,
		BreakevenAtR:     50, // 0.5R: −₹13.3k vs −₹22.1k at 1R (Mar–Sep 2026 option-model backtest), better in both halves

		// CALL only, top 20% after 60 pts: −₹56/−₹141 vs −₹91/−₹177 off per unit
		// (Mar–mid-Jun / mid-Jun–Oct 2026); blocking PUTs too lost in every setting.
		DayExtremePct: 80,
		DayRunPts:     6000,
		// From 10:30: +₹25/−₹137 vs −₹56/−₹141 from the open (10:00 −₹36/−₹140,
		// 11:00 −₹68/−₹167). A tighter retest touch (5 or 3 pts) lost in every
		// combination, so RetestTolPts stays 0 (= TouchTolPts, 10 pts).
		DayExtremeFromMin: hhmm(10, 30),

		// Sideways box, 30 min ≤ 1.5× 15m ATR: +₹59/−₹103 vs +₹25/−₹137 without
		// (Mar–mid-Jun / mid-Jun–Oct 2026, per unit). Neighbours are noisy
		// (6×100%: −41/−70, 6×200%: +6/−195, 9×150%: +106/−161); also
		// refusing fades scored +39/−98.
		BoxBars:   6,
		BoxATRPct: 150,
		// Warn, don't block: the block-mode numbers above are what
		// refusing these entries would have done.
		GateMode: SRGateWarn,

		EntryFromMin:    hhmm(9, 30),
		EntryToMin:      hhmm(14, 45),
		TimeExitMin:     hhmm(15, 10),
		MaxTradesPerDay: 3,
		MaxConsecLosses: 2,
		MaxDayLossPts:   6000, // 60 index pts
		CooldownCandles: 10,

		FNOHardSLPct:     20,
		FNOTrailStartPct: 25,
		FNOTrailSLPct:    10,

		StrikeStep: 50,

		IndexToken:   "NSE:99926000",
		NameOverride: "NIFTY50_SR",
	}
}

// nifty50SRState is the per-index state.
type nifty50SRState struct {
	ctx *srContext

	// ── Position ──
	Side        PositionSide
	Kind        string
	Regime      SRRegime
	IndexEntry  int64
	StopLevel   int64
	TargetLevel int64
	Risk        int64     // |entry − initial stop|
	Breakeven   bool      // stop moved to entry
	EntryTS     time.Time // bucket start of the entry bar
	MaxGain     int64     // best 1m-close gain since entry, index paise

	Strike        int64
	FNOToken      string
	FNOEntryPrice int64
	FNOBestPrice  int64

	// ── Armed breakout awaiting its retest ──
	PendingSide  PositionSide // SideCall = broke up, SidePut = broke down
	PendingLevel int64
	PendingLeft  int // entry bars left

	// ── Day ──
	TradeDay     string
	TradesToday  int
	ConsecLosses int
	DayPnLPts    int64        // closed trades, index paise, sign-adjusted
	LastSide     PositionSide // side of today's last entry
	LastADX      float64      // 15m ADX at that entry
	CooldownLeft int
	LastClose    int64

	LastCloseTS time.Time
}

func newNifty50SRState(cfg Nifty50SRConfig) *nifty50SRState {
	return &nifty50SRState{ctx: newSRContext(cfg.Context), Side: SideNone, PendingSide: SideNone}
}

// ── Snapshot / Restore ──

type nifty50SRSnapshot struct {
	Key     string            `json:"key"`
	Context srContextSnapshot `json:"context"`

	Side          PositionSide `json:"side"`
	Kind          string       `json:"kind"`
	Regime        SRRegime     `json:"regime"`
	IndexEntry    int64        `json:"index_entry"`
	StopLevel     int64        `json:"stop_level"`
	TargetLevel   int64        `json:"target_level"`
	Risk          int64        `json:"risk"`
	Breakeven     bool         `json:"breakeven"`
	EntryTS       time.Time    `json:"entry_ts,omitempty"`
	MaxGain       int64        `json:"max_gain,omitempty"`
	Strike        int64        `json:"strike"`
	FNOToken      string       `json:"fno_token"`
	FNOEntryPrice int64        `json:"fno_entry_price"`
	FNOBestPrice  int64        `json:"fno_best_price"`

	PendingSide  PositionSide `json:"pending_side"`
	PendingLevel int64        `json:"pending_level"`
	PendingLeft  int          `json:"pending_left"`

	TradeDay     string       `json:"trade_day"`
	TradesToday  int          `json:"trades_today"`
	ConsecLosses int          `json:"consec_losses"`
	DayPnLPts    int64        `json:"day_pnl_pts"`
	LastSide     PositionSide `json:"last_side,omitempty"`
	LastADX      float64      `json:"last_adx,omitempty"`
	CooldownLeft int          `json:"cooldown_left"`
	LastClose    int64        `json:"last_close"`

	LastCloseTS time.Time `json:"last_close_ts"`
}

type nifty50SRStrategySnapshot struct {
	Version     int                 `json:"version"`
	Strategy    string              `json:"strategy"`
	Instruments []nifty50SRSnapshot `json:"instruments"`
}

const nifty50SRSnapshotVersion = 1

func snapshotNifty50SR(key string, s *nifty50SRState) nifty50SRSnapshot {
	return nifty50SRSnapshot{
		Key: key, Context: s.ctx.snapshot(),
		Side: s.Side, Kind: s.Kind, Regime: s.Regime, IndexEntry: s.IndexEntry,
		StopLevel: s.StopLevel, TargetLevel: s.TargetLevel, Risk: s.Risk, Breakeven: s.Breakeven,
		EntryTS: s.EntryTS, MaxGain: s.MaxGain,
		Strike: s.Strike, FNOToken: s.FNOToken, FNOEntryPrice: s.FNOEntryPrice, FNOBestPrice: s.FNOBestPrice,
		PendingSide: s.PendingSide, PendingLevel: s.PendingLevel, PendingLeft: s.PendingLeft,
		TradeDay: s.TradeDay, TradesToday: s.TradesToday, ConsecLosses: s.ConsecLosses,
		DayPnLPts: s.DayPnLPts, LastSide: s.LastSide, LastADX: s.LastADX, CooldownLeft: s.CooldownLeft, LastClose: s.LastClose,
		LastCloseTS: s.LastCloseTS,
	}
}

func restoreNifty50SR(cfg Nifty50SRConfig, snap nifty50SRSnapshot) *nifty50SRState {
	s := &nifty50SRState{
		ctx:  restoreSRContext(cfg.Context, snap.Context),
		Side: snap.Side, Kind: snap.Kind, Regime: snap.Regime, IndexEntry: snap.IndexEntry,
		StopLevel: snap.StopLevel, TargetLevel: snap.TargetLevel, Risk: snap.Risk, Breakeven: snap.Breakeven,
		EntryTS: snap.EntryTS, MaxGain: snap.MaxGain,
		Strike: snap.Strike, FNOToken: snap.FNOToken, FNOEntryPrice: snap.FNOEntryPrice, FNOBestPrice: snap.FNOBestPrice,
		PendingSide: snap.PendingSide, PendingLevel: snap.PendingLevel, PendingLeft: snap.PendingLeft,
		TradeDay: snap.TradeDay, TradesToday: snap.TradesToday, ConsecLosses: snap.ConsecLosses,
		DayPnLPts: snap.DayPnLPts, LastSide: snap.LastSide, LastADX: snap.LastADX, CooldownLeft: snap.CooldownLeft, LastClose: snap.LastClose,
		LastCloseTS: snap.LastCloseTS,
	}
	if s.Side == "" {
		s.Side = SideNone
	}
	if s.PendingSide == "" {
		s.PendingSide = SideNone
	}
	return s
}

func marshalNifty50SRSnapshot(name string, instruments map[string]*nifty50SRState) ([]byte, error) {
	snap := nifty50SRStrategySnapshot{Version: nifty50SRSnapshotVersion, Strategy: name}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50SR(k, v))
	}
	return json.Marshal(snap)
}
