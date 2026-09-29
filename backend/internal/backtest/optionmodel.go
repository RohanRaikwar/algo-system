package backtest

import (
	"math"
	"time"

	"trading-systemv1/internal/optionmath"
	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  Modeled option premiums for backtests.
//
//  Angel serves no history for expired option contracts, so a backtest
//  can't replay real premiums for past trades. Instead each trade's own
//  contract (its strike, CE/PE, the weekly Tuesday expiry the live picker
//  would trade — never same-day) is priced with Black-Scholes from the
//  minute's NIFTY spot and an IV (India VIX history when loaded, else a
//  flat IV). This captures delta, gamma and theta; it does not capture
//  real IV skew, IV spikes or bid/ask beyond the paper slippage model.
//  Float math stays inside the model; results are rounded to paise.
// ════════════════════════════════════════════════════════════════════

// OptionModel configures modeled premiums (zero value = off).
type OptionModel struct {
	Enabled        bool
	IVPct          float64 // flat IV % when no VIX point is available (e.g. 13)
	RatePct        float64 // risk-free rate % (e.g. 6.5)
	PremiumSLPct   int64   // exit when premium falls this % below entry (live hard SL), 0 = off
	SlippageBps    int64   // paper slippage, as the live executor
	SlippageMinPsa int64
	StrikeStep     int64 // 50
}

// weeklyExpiry returns the NIFTY weekly expiry (Tuesday 15:30 IST) the live
// picker trades at ts: this week's Tuesday, or next week's on a Tuesday.
func weeklyExpiry(ts time.Time) time.Time {
	t := ts.In(istLoc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, istLoc)
	d := (int(time.Tuesday) - int(day.Weekday()) + 7) % 7
	if d == 0 {
		d = 7 // never trade same-day expiry
	}
	return day.AddDate(0, 0, d).Add(15*time.Hour + 30*time.Minute)
}

// premium models the option price (paise) at ts for the nearest weekly
// expiry after today; no slippage.
func (m OptionModel) premium(side strategy.PositionSide, strikePts, spotPaise int64, ts time.Time, ivPct float64) int64 {
	return m.premiumTo(side, strikePts, spotPaise, ts, ivPct, weeklyExpiry(ts))
}

// sameDayExpiry is today's 15:30 IST, for expiry-day (gamma) contracts.
func sameDayExpiry(ts time.Time) time.Time {
	t := ts.In(istLoc)
	return time.Date(t.Year(), t.Month(), t.Day(), 15, 30, 0, 0, istLoc)
}

// premiumTo models the option price (paise) at ts for a given expiry.
func (m OptionModel) premiumTo(side strategy.PositionSide, strikePts, spotPaise int64, ts time.Time, ivPct float64, expiry time.Time) int64 {
	if spotPaise <= 0 || strikePts <= 0 {
		return 0
	}
	if ivPct <= 0 {
		ivPct = m.IVPct
	}
	years := expiry.Sub(ts).Hours() / (24 * 365)
	p := optionmath.Price(float64(spotPaise)/100, float64(strikePts), years, ivPct/100, m.RatePct/100, side == strategy.SideCall)
	paise := int64(math.Round(p * 100))
	if paise < 5 {
		paise = 5 // one tick
	}
	return paise
}

// slip applies paper slippage: buy pays more, sell gets less.
func (m OptionModel) slip(buy bool, p int64) int64 {
	s := p * m.SlippageBps / 10000
	if s < m.SlippageMinPsa {
		s = m.SlippageMinPsa
	}
	if buy {
		return p + s
	}
	if p-s > 5 {
		return p - s
	}
	return 5
}

// atmStrike rounds index paise to the nearest strike.
func (m OptionModel) atmStrike(spotPaise int64) int64 {
	step := m.StrikeStep
	if step <= 0 {
		step = 50
	}
	unit := step * 100
	return (spotPaise + unit/2) / unit * step
}
