package strategy

// ScalePoints returns c with every index-paise distance converted by
// scale, for running the NIFTY-tuned SR strategy on another index (SENSEX
// moves about 3.3× NIFTY's points). Percentages, ATR fractions, RSI/ADX
// levels, bar counts and times are scale-free and stay as they are.
func (c Nifty50SRConfig) ScalePoints(scale func(paise int64) int64) Nifty50SRConfig {
	c.TouchTolPts = scale(c.TouchTolPts)
	c.FlatEMAPts = scale(c.FlatEMAPts)
	c.StopMinPts = scale(c.StopMinPts)
	c.SupportStopZonePts = scale(c.SupportStopZonePts)
	c.DayRunPts = scale(c.DayRunPts)
	c.RetestTolPts = scale(c.RetestTolPts)
	c.MaxDayLossPts = scale(c.MaxDayLossPts)

	c.Context.FlatVWAPSlopePts = scale(c.Context.FlatVWAPSlopePts)
	c.Context.MinATRPts = scale(c.Context.MinATRPts)

	r := &c.Context.Range
	r.FlatSlopePts = scale(r.FlatSlopePts)
	r.LevelTolerancePts = scale(r.LevelTolerancePts)
	r.MinRangePts = scale(r.MinRangePts)
	r.MaxRangePts = scale(r.MaxRangePts)
	r.NarrowMaxPts = scale(r.NarrowMaxPts)
	r.MediumMaxPts = scale(r.MediumMaxPts)
	return c
}
