package backtest

import (
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

// ── P&L Calculation Tests ──────────────────────────────────────────

func TestTrade_PnL_Call(t *testing.T) {
	tr := Trade{
		Side:       strategy.SideCall,
		EntryPrice: 10000, // 100.00 ₹
		ExitPrice:  10500, // 105.00 ₹
	}
	if got := tr.PnLPaise(); got != 500 {
		t.Errorf("CALL PnL paise: want 500, got %d", got)
	}
	if got := tr.PnLRupees(); got != 5.0 {
		t.Errorf("CALL PnL rupees: want 5.0, got %.2f", got)
	}
}

func TestTrade_PnL_Put(t *testing.T) {
	tr := Trade{
		Side:       strategy.SidePut,
		EntryPrice: 10500, // 105.00 ₹
		ExitPrice:  10000, // 100.00 ₹
	}
	if got := tr.PnLPaise(); got != 500 {
		t.Errorf("PUT PnL paise: want 500, got %d", got)
	}
}

func TestTrade_PnL_Loss(t *testing.T) {
	tr := Trade{
		Side:       strategy.SideCall,
		EntryPrice: 10500,
		ExitPrice:  10000,
	}
	if got := tr.PnLPaise(); got != -500 {
		t.Errorf("CALL loss PnL paise: want -500, got %d", got)
	}
}

// ── Metrics Computation Tests ──────────────────────────────────────

func TestComputeMetrics_BasicStats(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 10000, ExitPrice: 10500, EntryTime: base, ExitTime: base.Add(10 * time.Minute)},
		{Side: strategy.SidePut, EntryPrice: 10500, ExitPrice: 10200, EntryTime: base.Add(20 * time.Minute), ExitTime: base.Add(30 * time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 10300, ExitPrice: 10100, EntryTime: base.Add(40 * time.Minute), ExitTime: base.Add(50 * time.Minute)},
	}

	m := computeMetrics(trades, 1)

	if m.TotalTrades != 3 {
		t.Errorf("total trades: want 3, got %d", m.TotalTrades)
	}
	if m.CallTrades != 2 {
		t.Errorf("call trades: want 2, got %d", m.CallTrades)
	}
	if m.PutTrades != 1 {
		t.Errorf("put trades: want 1, got %d", m.PutTrades)
	}
	if m.Wins != 2 {
		t.Errorf("wins: want 2, got %d", m.Wins)
	}
	if m.Losses != 1 {
		t.Errorf("losses: want 1, got %d", m.Losses)
	}
}

func TestComputeMetrics_WinRate(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: base, ExitTime: base.Add(time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 500, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1200, EntryTime: base.Add(4 * time.Minute), ExitTime: base.Add(5 * time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 800, EntryTime: base.Add(6 * time.Minute), ExitTime: base.Add(7 * time.Minute)},
	}

	m := computeMetrics(trades, 1)
	if m.WinRate != 50.0 {
		t.Errorf("win rate: want 50.0%%, got %.1f%%", m.WinRate)
	}
}

func TestComputeMetrics_ProfitFactor(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 2000, EntryTime: base, ExitTime: base.Add(time.Minute)},                         // +10.00
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 500, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)}, // -5.00
	}

	m := computeMetrics(trades, 1)
	if m.ProfitFactor != 2.0 {
		t.Errorf("profit factor: want 2.0, got %.2f", m.ProfitFactor)
	}
}

func TestComputeMetrics_ZeroTrades(t *testing.T) {
	m := computeMetrics(nil, 1)
	if m.TotalTrades != 0 {
		t.Errorf("zero trades: total should be 0, got %d", m.TotalTrades)
	}
	if m.WinRate != 0 {
		t.Error("zero trades: win rate should be 0")
	}
}

func TestComputeMetrics_AllWins(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: base, ExitTime: base.Add(time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1300, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)},
	}

	m := computeMetrics(trades, 1)
	if m.WinRate != 100.0 {
		t.Errorf("all wins: win rate should be 100, got %.1f", m.WinRate)
	}
	if m.Losses != 0 {
		t.Errorf("all wins: losses should be 0, got %d", m.Losses)
	}
}

func TestComputeMetrics_AllLosses(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1500, ExitPrice: 1000, EntryTime: base, ExitTime: base.Add(time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 1300, ExitPrice: 1000, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)},
	}

	m := computeMetrics(trades, 1)
	if m.WinRate != 0 {
		t.Errorf("all losses: win rate should be 0, got %.1f", m.WinRate)
	}
	if m.Wins != 0 {
		t.Errorf("all losses: wins should be 0, got %d", m.Wins)
	}
}

// ── Max Drawdown Tests ─────────────────────────────────────────────

func TestComputeMaxDrawdown(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		name   string
		trades []Trade
		qty    int64
		wantDD float64
	}{
		{
			name:   "no trades",
			trades: nil,
			qty:    1,
			wantDD: 0,
		},
		{
			name: "all wins → no drawdown",
			trades: []Trade{
				{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: base, ExitTime: base.Add(time.Minute)},
				{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1200, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)},
			},
			qty:    1,
			wantDD: 0,
		},
		{
			name: "win then loss",
			trades: []Trade{
				{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 2000, EntryTime: base, ExitTime: base.Add(time.Minute)},                          // +10
				{Side: strategy.SideCall, EntryPrice: 2000, ExitPrice: 1500, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)}, // -5
			},
			qty:    1,
			wantDD: -5.0,
		},
		{
			name: "multiple losses",
			trades: []Trade{
				{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 2000, EntryTime: base, ExitTime: base.Add(time.Minute)},                          // +10
				{Side: strategy.SideCall, EntryPrice: 2000, ExitPrice: 1000, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)}, // -10
				{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 500, EntryTime: base.Add(4 * time.Minute), ExitTime: base.Add(5 * time.Minute)},  // -5
			},
			qty:    1,
			wantDD: -15.0, // peak at 10, trough at -5 → DD = -15
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeMaxDrawdown(tt.trades, tt.qty)
			if math.Abs(got-tt.wantDD) > 0.01 {
				t.Errorf("max drawdown: want %.2f, got %.2f", tt.wantDD, got)
			}
		})
	}
}

// ── Equity Curve Tests ─────────────────────────────────────────────

func TestComputeEquityCurve(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: base, ExitTime: base.Add(time.Minute)},                          // +5
		{Side: strategy.SideCall, EntryPrice: 1500, ExitPrice: 1200, EntryTime: base.Add(2 * time.Minute), ExitTime: base.Add(3 * time.Minute)}, // -3
		{Side: strategy.SidePut, EntryPrice: 1200, ExitPrice: 1000, EntryTime: base.Add(4 * time.Minute), ExitTime: base.Add(5 * time.Minute)},  // +2
	}

	curve := computeEquityCurve(trades, 1)
	want := []float64{5.0, 2.0, 4.0}

	if len(curve) != len(want) {
		t.Fatalf("equity curve length: want %d, got %d", len(want), len(curve))
	}
	for i := range want {
		if math.Abs(curve[i]-want[i]) > 0.01 {
			t.Errorf("equity curve[%d]: want %.2f, got %.2f", i, want[i], curve[i])
		}
	}
}

// ── Per-Day Summary Tests ──────────────────────────────────────────

func TestComputeDaySummaries(t *testing.T) {
	// IST = UTC+5:30, so 09:30 IST = 04:00 UTC
	day1 := time.Date(2026, 3, 5, 4, 0, 0, 0, time.UTC) // 09:30 IST
	day2 := time.Date(2026, 3, 6, 4, 0, 0, 0, time.UTC) // 09:30 IST next day

	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: day1, ExitTime: day1.Add(10 * time.Minute)},
		{Side: strategy.SideCall, EntryPrice: 1500, ExitPrice: 1200, EntryTime: day1.Add(20 * time.Minute), ExitTime: day1.Add(30 * time.Minute)},
		{Side: strategy.SidePut, EntryPrice: 2000, ExitPrice: 1500, EntryTime: day2, ExitTime: day2.Add(10 * time.Minute)},
	}

	sums := computeDaySummaries(trades, 1)

	if len(sums) != 2 {
		t.Fatalf("day summaries: want 2 days, got %d", len(sums))
	}

	if sums[0].Trades != 2 {
		t.Errorf("day1 trades: want 2, got %d", sums[0].Trades)
	}
	if sums[1].Trades != 1 {
		t.Errorf("day2 trades: want 1, got %d", sums[1].Trades)
	}
}

// ── Sharpe Ratio Tests ─────────────────────────────────────────────

func TestComputeSharpeRatio_SingleTrade(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: base, ExitTime: base.Add(time.Minute)},
	}

	// Single trade → not enough data → 0
	sr := computeSharpeRatio(trades, 1)
	if sr != 0 {
		t.Errorf("single trade Sharpe should be 0, got %.2f", sr)
	}
}

// ── Duration Tests ─────────────────────────────────────────────────

func TestTrade_Duration(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	tr := Trade{
		EntryTime: base,
		ExitTime:  base.Add(15 * time.Minute),
	}
	if got := tr.Duration(); got != 15*time.Minute {
		t.Errorf("duration: want 15m, got %v", got)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5*time.Minute + 30*time.Second, "5m 30s"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v): want %q, got %q", tt.d, tt.want, got)
		}
	}
}

// ── Qty Multiplier Tests ───────────────────────────────────────────

func TestComputeMetrics_WithQty(t *testing.T) {
	base := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	trades := []Trade{
		{Side: strategy.SideCall, EntryPrice: 1000, ExitPrice: 1500, EntryTime: base, ExitTime: base.Add(time.Minute)}, // +5 per unit
	}

	m := computeMetrics(trades, 10)
	// 5 * 10 = 50
	if m.NetPnL != 50.0 {
		t.Errorf("net P&L with qty=10: want 50.0, got %.2f", m.NetPnL)
	}
}
