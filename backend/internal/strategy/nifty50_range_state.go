package strategy

import (
	"encoding/json"
	"time"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 RANGE — range-market strategy (NIFTY_RANGE_MARKET_GUIDE.md)
//
//  S1 mean reversion: BUY CALL at support / PUT at resistance on a 1m
//     reversal candle with 5m RSI and volume confirmation.
//  S2 breakout: BUY in the break direction after a 1m close beyond the
//     range edge, with volume and a follow-through bar.
//
//  Situation (range, levels, ADX, RSI) comes from 5m/15m bars built
//  from the 1m stream (see range_context.go). Index levels are int64 paise.
// ════════════════════════════════════════════════════════════════════

// Entry kinds recorded on a position.
const (
	RangeKindMeanReversion = "MEAN_REVERSION"
	RangeKindBreakout      = "BREAKOUT"
	RangeKindFlag          = "FLAG" // consolidation breakout in the day's trend direction
)

// timeWindow is [From, To) in minutes after midnight IST.
type timeWindow struct {
	From int
	To   int
}

func inWindows(min int, ws []timeWindow) bool {
	for _, w := range ws {
		if min >= w.From && min < w.To {
			return true
		}
	}
	return false
}

// Nifty50RangeConfig holds all configurable parameters.
type Nifty50RangeConfig struct {
	Range RangeContextConfig

	// ── S1 mean reversion ──
	MeanReversionEnabled bool
	EntryZonePct         int64   // % of range width from an edge that counts as "at the edge"
	RSIBuyMax            float64 // 5m RSI must be below this for CALL (guide: 45)
	RSISellMin           float64 // 5m RSI must be above this for PUT (guide: 55)
	ReversalVolPct       int64   // reversal bar volume ≥ this % of average
	RequireVolume        bool    // false: skip volume checks when no volume data (index)
	RequirePrevOpposite  bool    // previous entry bar must be bearish (CALL) / bullish (PUT)
	StopBufferPts        int64   // minimum paise beyond the edge for the index stop
	StopATRPct           int64   // stop buffer ≥ this % of 15m ATR (noise-sized stop), 0 = fixed only

	// EntryTFMinutes is the bar entries are judged on (5 = guide's "5-min
	// entry"; 1 = every 1m close). Stops, targets and the time exit are
	// always checked on 1m closes.
	EntryTFMinutes int

	// HTFTrendFilter takes breakouts and flags only with the 1h trend
	// (price above the 1h EMA for CALL, below for PUT).
	HTFTrendFilter bool

	// VolumeToken is an instrument whose ticks supply volume ("NFO:<fut
	// token>", the NIFTY future). The index has none; "" = no volume data.
	VolumeToken    string
	MeanRevWindows []timeWindow

	// ── S2 breakout ──
	BreakoutEnabled    bool
	BreakoutConfirmPts int64 // paise beyond the edge for a breakout close
	BreakoutVolPct     int64 // breakout bar volume ≥ this % of average
	BreakoutStopPts    int64 // paise back inside the range for the stop
	BreakoutTargetPct  int64 // target = edge ± this % of range width (trail arms at half of it)
	BreakoutWindows    []timeWindow

	// ── Option selection ──
	// Each entry names its own strike from the signal close, so a position
	// never inherits a stale 09:15 ATM. OTMSteps shifts it out of the money
	// by range class (guide: narrow ATM only, wide 1-2 OTM); breakouts use
	// BreakoutOTMSteps.
	StrikeStep       int64
	OTMSteps         map[RangeClass]int
	BreakoutOTMSteps int
	ATMWithinDTE     int // at or below this many days to expiry, always buy ATM

	// ── S2b flag: consolidation breakout on trend days ──
	// When the range regime is off (15m ADX high after a strong move), a
	// tight 5m box that forms in the day's trend and then breaks in the
	// trend direction is traded like a breakout. Target = FlagTargetPolePct
	// of the day's move ("pole") projected from the box edge.
	FlagEnabled       bool
	FlagBars          int   // 5m bars in the box (12 = 1 hour)
	FlagMaxWidthBps   int64 // box height ≤ this many bps of price (30 = 0.30%)
	FlagMinTrendPts   int64 // |close − day open| ≥ this (paise) for a trend day
	FlagTargetPolePct int64
	FlagWindows       []timeWindow

	// ── Exits / risk ──
	MinRewardRiskPct int64 // index reward ≥ this % of index risk (120 = 1.2:1), 0 = off
	TimeExitMin      int   // minutes after midnight IST; 15:00 (before NSE's closing auction session)
	MaxTradesPerDay  int   // guide: "don't overtrade"
	CooldownCandles  int   // 1m candles after an exit before a new entry
	FNOHardSLPct     int64 // premium hard SL % (guide: 15-20% of premium)
	FNOTargetPct     int64 // premium target %, 0 = off (index targets decide)
	FNOTrailStartPct int64 // premium gain % that arms the trailing SL
	FNOTrailSLPct    int64 // premium drop % from best once armed

	// ── FNO token identifiers ──
	FNOCallToken string
	FNOPutToken  string

	// ── Candle filter ──
	IndexToken string // e.g. "NSE:99926000"

	NameOverride string
}

// DefaultNifty50RangeConfig returns the guide's values.
func DefaultNifty50RangeConfig() Nifty50RangeConfig {
	rc := DefaultRangeContextConfig()
	// NIFTY's 15m ADX sits ≥ 25 on ~60% of bars; the guide's 25 starved the
	// strategy. 30 was chosen on Mar-Jun 2026 and held on Jul-Sep 2026.
	rc.MaxADX = 30
	return Nifty50RangeConfig{
		Range: rc,

		// Off: the 2026-03..09 backtest lost on mean reversion (8 trades,
		// -79 pts) while breakouts carried the result (28 trades, +440 pts).
		MeanReversionEnabled: false,
		EntryZonePct:         20,
		RSIBuyMax:            45,
		RSISellMin:           55,
		ReversalVolPct:       120,
		RequireVolume:        false,
		RequirePrevOpposite:  false, // hammer/engulfing already encode the reversal
		StopBufferPts:        2500,  // ≥ 25 pts beyond the level
		StopATRPct:           50,    // …or half a 15m ATR, whichever is wider
		EntryTFMinutes:       5,
		MeanRevWindows: []timeWindow{
			{From: hhmm(9, 45), To: hhmm(11, 30)},
			{From: hhmm(14, 0), To: hhmm(14, 45)},
		},

		BreakoutEnabled:    true,
		BreakoutConfirmPts: 1000, // 10 pts
		BreakoutVolPct:     150,
		BreakoutStopPts:    2500, // ≥ 25 pts back inside the range (or half a 15m ATR, if wider)
		// Full measured move: a 50% target cut trades (reward:risk) and win
		// rate in the Mar-Jun 2026 backtest; time exits bank what's left.
		BreakoutTargetPct: 100,
		BreakoutWindows: []timeWindow{
			{From: hhmm(9, 45), To: hhmm(14, 45)}, // 15 min before the 15:00 exit
		},

		StrikeStep:       50,
		OTMSteps:         map[RangeClass]int{RangeNarrow: 0, RangeMedium: 0, RangeWide: 1},
		BreakoutOTMSteps: 0,
		ATMWithinDTE:     2, // OTM delta collapses near expiry

		MinRewardRiskPct:  120,
		FlagEnabled:       true,
		FlagBars:          12,
		FlagMaxWidthBps:   30,
		FlagMinTrendPts:   10000, // 100 pts
		FlagTargetPolePct: 50,
		FlagWindows:       []timeWindow{{From: hhmm(10, 15), To: hhmm(14, 45)}},

		// Flat by 15:00: NSE's closing auction session makes the last half hour erratic.
		TimeExitMin:      hhmm(15, 0),
		MaxTradesPerDay:  4,
		CooldownCandles:  5,
		FNOHardSLPct:     20,
		FNOTargetPct:     0,
		FNOTrailStartPct: 25,
		FNOTrailSLPct:    10,

		IndexToken:   "NSE:99926000",
		NameOverride: "NIFTY50_RANGE",
	}
}

// targetFraction returns the % of range width used as the S1 target.
// The executor has one quantity per position, so the guide's partial
// bookings (50% / 75% / 100%) collapse to one exit per range class;
// the trailing stop armed at 50% protects the rest of the move.
func targetFraction(c RangeClass) int64 {
	switch c {
	case RangeNarrow:
		return 50
	case RangeMedium:
		return 75
	default:
		return 100
	}
}

// atmStrike rounds an index price (paise) to the nearest strike (points).
func atmStrike(pricePaise, step int64) int64 {
	unit := step * 100
	return (pricePaise + unit/2) / unit * step
}

// entryStrike is the strike to buy: ATM shifted otm steps out of the money
// (up for a CALL, down for a PUT).
func entryStrike(side PositionSide, pricePaise, step int64, otm int) int64 {
	k := atmStrike(pricePaise, step)
	if side == SidePut {
		return k - int64(otm)*step
	}
	return k + int64(otm)*step
}

// nifty50RangeState is the per-index state.
type nifty50RangeState struct {
	ctx *rangeContext

	// ── Position ──
	Side        PositionSide
	Kind        string
	IndexEntry  int64
	StopLevel   int64
	TargetLevel int64
	TrailArm    int64 // index level that arms the index trailing stop
	TrailArmed  bool
	IndexBest   int64 // best index close since entry (in trade direction)
	RangeSup    int64 // range at entry
	RangeRes    int64

	// ── FNO tracking ──
	Strike        int64  // strike asked for at entry (points)
	FNOToken      string // "NFO:<token>" of the held contract, once known
	FNOEntryPrice int64
	FNOBestPrice  int64

	// ── Last range seen with price inside it (breakout reference) ──
	LastSup, LastRes, LastWidth int64

	// ── Breakout tracking ──
	PendingBreak PositionSide // SideCall = above resistance, SidePut = below support
	PendingEdge  int64
	PendingWidth int64  // target distance from the edge (paise) once confirmed
	PendingKind  string // RangeKindBreakout or RangeKindFlag

	// ── Session ──
	DayOpen   int64 // first 1m open of the session
	BrokenSup int64 // levels of a range that broke; not traded again
	BrokenRes int64

	// ── Pacing ──
	TradeDay     string
	TradesToday  int
	CooldownLeft int

	LastCloseTS time.Time
}

func newNifty50RangeState(cfg Nifty50RangeConfig) *nifty50RangeState {
	return &nifty50RangeState{ctx: newRangeContext(rangeCtxCfg(cfg)), Side: SideNone}
}

// rangeCtxCfg is cfg.Range with the entry series matching EntryTFMinutes.
func rangeCtxCfg(cfg Nifty50RangeConfig) RangeContextConfig {
	rc := cfg.Range
	rc.EntryMinutes = cfg.EntryTFMinutes
	return rc
}

// ── Snapshot / Restore ──

type nifty50RangeSnapshot struct {
	Key string `json:"key"`

	Context rangeContextSnapshot `json:"context"`

	Side        PositionSide `json:"side"`
	Kind        string       `json:"kind"`
	IndexEntry  int64        `json:"index_entry"`
	StopLevel   int64        `json:"stop_level"`
	TargetLevel int64        `json:"target_level"`
	TrailArm    int64        `json:"trail_arm"`
	TrailArmed  bool         `json:"trail_armed"`
	IndexBest   int64        `json:"index_best"`
	RangeSup    int64        `json:"range_sup"`
	RangeRes    int64        `json:"range_res"`

	Strike        int64  `json:"strike"`
	FNOToken      string `json:"fno_token"`
	FNOEntryPrice int64  `json:"fno_entry_price"`
	FNOBestPrice  int64  `json:"fno_best_price"`

	LastSup   int64 `json:"last_sup"`
	LastRes   int64 `json:"last_res"`
	LastWidth int64 `json:"last_width"`

	PendingBreak PositionSide `json:"pending_break"`
	PendingEdge  int64        `json:"pending_edge"`
	PendingWidth int64        `json:"pending_width"`
	PendingKind  string       `json:"pending_kind"`
	DayOpen      int64        `json:"day_open"`
	BrokenSup    int64        `json:"broken_sup"`
	BrokenRes    int64        `json:"broken_res"`

	TradeDay     string `json:"trade_day"`
	TradesToday  int    `json:"trades_today"`
	CooldownLeft int    `json:"cooldown_left"`

	LastCloseTS time.Time `json:"last_close_ts"`
}

type nifty50RangeStrategySnapshot struct {
	Version     int                    `json:"version"`
	Strategy    string                 `json:"strategy"`
	Instruments []nifty50RangeSnapshot `json:"instruments"`
}

// nifty50RangeSnapshotVersion 2: int64 levels + multi-TF context.
// Version 1 snapshots (float64 levels) are dropped on restore.
const nifty50RangeSnapshotVersion = 2

func snapshotNifty50Range(key string, s *nifty50RangeState) nifty50RangeSnapshot {
	return nifty50RangeSnapshot{
		Key: key, Context: s.ctx.snapshot(),
		Side: s.Side, Kind: s.Kind, IndexEntry: s.IndexEntry,
		StopLevel: s.StopLevel, TargetLevel: s.TargetLevel,
		TrailArm: s.TrailArm, TrailArmed: s.TrailArmed, IndexBest: s.IndexBest,
		RangeSup: s.RangeSup, RangeRes: s.RangeRes,
		Strike: s.Strike, FNOToken: s.FNOToken,
		FNOEntryPrice: s.FNOEntryPrice, FNOBestPrice: s.FNOBestPrice,
		LastSup: s.LastSup, LastRes: s.LastRes, LastWidth: s.LastWidth,
		PendingBreak: s.PendingBreak, PendingEdge: s.PendingEdge, PendingWidth: s.PendingWidth,
		PendingKind: s.PendingKind, DayOpen: s.DayOpen,
		BrokenSup: s.BrokenSup, BrokenRes: s.BrokenRes,
		TradeDay: s.TradeDay, TradesToday: s.TradesToday, CooldownLeft: s.CooldownLeft,
		LastCloseTS: s.LastCloseTS,
	}
}

func restoreNifty50Range(cfg Nifty50RangeConfig, snap nifty50RangeSnapshot) *nifty50RangeState {
	s := &nifty50RangeState{
		ctx:  restoreRangeContext(rangeCtxCfg(cfg), snap.Context),
		Side: snap.Side, Kind: snap.Kind, IndexEntry: snap.IndexEntry,
		StopLevel: snap.StopLevel, TargetLevel: snap.TargetLevel,
		TrailArm: snap.TrailArm, TrailArmed: snap.TrailArmed, IndexBest: snap.IndexBest,
		RangeSup: snap.RangeSup, RangeRes: snap.RangeRes,
		Strike: snap.Strike, FNOToken: snap.FNOToken,
		FNOEntryPrice: snap.FNOEntryPrice, FNOBestPrice: snap.FNOBestPrice,
		LastSup: snap.LastSup, LastRes: snap.LastRes, LastWidth: snap.LastWidth,
		PendingBreak: snap.PendingBreak, PendingEdge: snap.PendingEdge, PendingWidth: snap.PendingWidth,
		PendingKind: snap.PendingKind, DayOpen: snap.DayOpen,
		BrokenSup: snap.BrokenSup, BrokenRes: snap.BrokenRes,
		TradeDay: snap.TradeDay, TradesToday: snap.TradesToday, CooldownLeft: snap.CooldownLeft,
		LastCloseTS: snap.LastCloseTS,
	}
	if s.Side == "" {
		s.Side = SideNone
	}
	if s.PendingBreak == "" {
		s.PendingBreak = SideNone
	}
	return s
}

func marshalNifty50RangeSnapshot(name string, instruments map[string]*nifty50RangeState) ([]byte, error) {
	snap := nifty50RangeStrategySnapshot{Version: nifty50RangeSnapshotVersion, Strategy: name}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50Range(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty50RangeSnapshot(data []byte) (*nifty50RangeStrategySnapshot, error) {
	var snap nifty50RangeStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// SetBreakoutWindowEnd sets when new breakout entries stop (HH, MM IST).
func (c *Nifty50RangeConfig) SetBreakoutWindowEnd(h, m int) {
	for i := range c.BreakoutWindows {
		c.BreakoutWindows[i].To = hhmm(h, m)
	}
}
