// Package optionmath holds Black-Scholes pricing and greeks shared by the
// backtest option model and the live option picker. Prices and greeks are in
// the unit of spot/strike (index points = rupees for NIFTY options).
package optionmath

import (
	"math"
	"time"
)

var ist = time.FixedZone("IST", 5*3600+30*60)

func normCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }
func normPDF(x float64) float64 { return math.Exp(-x*x/2) / math.Sqrt(2*math.Pi) }

// Price is the Black-Scholes price of a European option.
func Price(spot, strike, years, iv, rate float64, call bool) float64 {
	if years <= 0 || iv <= 0 {
		if call {
			return math.Max(spot-strike, 0)
		}
		return math.Max(strike-spot, 0)
	}
	sq := iv * math.Sqrt(years)
	d1 := (math.Log(spot/strike) + (rate+iv*iv/2)*years) / sq
	d2 := d1 - sq
	if call {
		return spot*normCDF(d1) - strike*math.Exp(-rate*years)*normCDF(d2)
	}
	return strike*math.Exp(-rate*years)*normCDF(-d2) - spot*normCDF(-d1)
}

// Greeks: Theta per calendar day, Vega per 1 IV point.
type Greeks struct {
	Delta, Gamma, Theta, Vega float64
}

// GreeksAt returns Black-Scholes greeks. At expiry or with no IV the option
// is its intrinsic value: delta 1/0 (call) or −1/0 (put), others 0.
func GreeksAt(spot, strike, years, iv, rate float64, call bool) Greeks {
	if years <= 0 || iv <= 0 {
		itm := spot > strike
		if !call {
			itm = spot < strike
		}
		var d float64
		if itm {
			d = 1
		}
		if !call {
			d = -d
		}
		return Greeks{Delta: d}
	}
	sqT := math.Sqrt(years)
	sq := iv * sqT
	d1 := (math.Log(spot/strike) + (rate+iv*iv/2)*years) / sq
	d2 := d1 - sq
	pdf := normPDF(d1)
	disc := strike * math.Exp(-rate*years)
	g := Greeks{
		Gamma: pdf / (spot * sq),
		Vega:  spot * pdf * sqT / 100,
	}
	decay := -spot * pdf * iv / (2 * sqT)
	if call {
		g.Delta = normCDF(d1)
		g.Theta = (decay - rate*disc*normCDF(d2)) / 365
	} else {
		g.Delta = normCDF(d1) - 1
		g.Theta = (decay + rate*disc*normCDF(-d2)) / 365
	}
	return g
}

// YearsTo is the time from now to 15:30 IST on expiry's date, in years (≥ 0).
func YearsTo(expiry, now time.Time) float64 {
	e := expiry.In(ist)
	close := time.Date(e.Year(), e.Month(), e.Day(), 15, 30, 0, 0, ist)
	d := close.Sub(now)
	if d <= 0 {
		return 0
	}
	return d.Hours() / (365 * 24)
}
