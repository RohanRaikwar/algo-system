package strategy

// EntryMarketState is the live market regime seen by the strategy at entry time.
type EntryMarketState string

const (
	EntryMarketStateTrending EntryMarketState = "trending"
	EntryMarketStateSideways EntryMarketState = "sideways"
	EntryMarketStateChoppy   EntryMarketState = "choppy"
	EntryMarketStateRange    EntryMarketState = "range"
)

// EntryStrength is the normalized strength bucket used for option automation.
type EntryStrength string

const (
	EntryStrengthLow    EntryStrength = "low"
	EntryStrengthMedium EntryStrength = "medium"
	EntryStrengthHigh   EntryStrength = "high"
)

// EntryAutomationContext exposes the strategy's live market context for
// downstream expiry/strike selection.
type EntryAutomationContext struct {
	Available        bool
	MarketState      EntryMarketState
	TrendStrength    EntryStrength
	MomentumStrength EntryStrength
	TrendSlopePct    float64
	MomentumPct      float64
}
