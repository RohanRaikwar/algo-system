package portfolio

import (
	"encoding/json"
	"testing"
	"time"
)

// ══════════════════════════════════════════
// Portfolio tests
// ══════════════════════════════════════════

func TestOpenPosition_New(t *testing.T) {
	pf := New()
	pf.OpenPosition("99926000", "NSE", 10, 2200000) // 10 qty @ ₹22,000

	if pf.PositionCount() != 1 {
		t.Fatalf("expected 1 position, got %d", pf.PositionCount())
	}
	pos := pf.GetPositions()[0]
	if pos.Qty != 10 || pos.AvgPrice != 2200000 {
		t.Fatalf("unexpected position: qty=%d avg=%d", pos.Qty, pos.AvgPrice)
	}
}

func TestOpenPosition_Add(t *testing.T) {
	pf := New()
	pf.OpenPosition("99926000", "NSE", 10, 2200000) // 10 @ ₹22,000
	pf.OpenPosition("99926000", "NSE", 10, 2400000) // 10 @ ₹24,000

	if pf.PositionCount() != 1 {
		t.Fatal("expected single merged position")
	}
	pos := pf.GetPositions()[0]
	if pos.Qty != 20 {
		t.Fatalf("expected qty 20, got %d", pos.Qty)
	}
	// Weighted avg: (22000*10 + 24000*10) / 20 = 23000 → 2300000 paise
	if pos.AvgPrice != 2300000 {
		t.Fatalf("expected avg 2300000, got %d", pos.AvgPrice)
	}
}

func TestClosePosition(t *testing.T) {
	pf := New()
	pf.OpenPosition("99926000", "NSE", 5, 1000000)
	closed := pf.ClosePosition("99926000", "NSE")
	if closed == nil {
		t.Fatal("expected closed position")
	}
	if closed.Qty != 5 {
		t.Fatalf("expected qty 5, got %d", closed.Qty)
	}
	if pf.PositionCount() != 0 {
		t.Fatalf("expected 0 positions after close, got %d", pf.PositionCount())
	}
}

func TestClosePosition_NonExistent(t *testing.T) {
	pf := New()
	closed := pf.ClosePosition("XXXX", "NSE")
	if closed != nil {
		t.Fatal("expected nil for non-existent position")
	}
}

func TestTotalExposure(t *testing.T) {
	pf := New()
	pf.OpenPosition("AAA", "NSE", 10, 100000) // 10 * 100000 = 1000000
	pf.OpenPosition("BBB", "NSE", 5, 200000)  // 5 * 200000 = 1000000

	exp := pf.TotalExposure()
	if exp != 2000000 {
		t.Fatalf("expected exposure 2000000, got %d", exp)
	}
}

func TestPortfolio_Snapshot_Restore(t *testing.T) {
	pf := New()
	pf.OpenPosition("AAA", "NSE", 10, 100000)
	pf.OpenPosition("BBB", "NFO", 5, 200000)

	snap, err := pf.Snapshot()
	if err != nil {
		t.Fatalf("snapshot error: %v", err)
	}

	pf2 := New()
	if err := pf2.RestorePositions(snap); err != nil {
		t.Fatalf("restore error: %v", err)
	}

	if pf2.PositionCount() != 2 {
		t.Fatalf("expected 2 positions after restore, got %d", pf2.PositionCount())
	}
	if pf2.TotalExposure() != pf.TotalExposure() {
		t.Fatalf("exposure mismatch after restore: %d vs %d", pf2.TotalExposure(), pf.TotalExposure())
	}
}

// ══════════════════════════════════════════
// PnLTracker tests
// ══════════════════════════════════════════

func TestPnL_RecordTrade_BuySell(t *testing.T) {
	tracker := NewPnLTracker()

	// BUY 10 @ ₹1000
	tracker.RecordTrade(Trade{
		Token: "AAA", Exchange: "NSE", Action: "BUY",
		Qty: 10, Price: 100000, Timestamp: time.Now(),
	})
	if tracker.GetRealizedPnL() != 0 {
		t.Fatal("expected 0 realized PnL after buy")
	}

	// SELL 10 @ ₹1100 → profit = (110000-100000)*10 = 100000 paise
	realized := tracker.RecordTrade(Trade{
		Token: "AAA", Exchange: "NSE", Action: "SELL",
		Qty: 10, Price: 110000, Timestamp: time.Now(),
	})
	if realized != 100000 {
		t.Fatalf("expected realized PnL 100000, got %d", realized)
	}
	if tracker.GetRealizedPnL() != 100000 {
		t.Fatalf("expected total realized 100000, got %d", tracker.GetRealizedPnL())
	}
}

func TestPnL_DailyStats(t *testing.T) {
	tracker := NewPnLTracker()

	// Winning trade
	tracker.RecordTrade(Trade{Token: "A", Exchange: "NSE", Action: "BUY", Qty: 10, Price: 100, Timestamp: time.Now()})
	tracker.RecordTrade(Trade{Token: "A", Exchange: "NSE", Action: "SELL", Qty: 10, Price: 150, Timestamp: time.Now()})

	// Losing trade
	tracker.RecordTrade(Trade{Token: "B", Exchange: "NSE", Action: "BUY", Qty: 5, Price: 200, Timestamp: time.Now()})
	tracker.RecordTrade(Trade{Token: "B", Exchange: "NSE", Action: "SELL", Qty: 5, Price: 180, Timestamp: time.Now()})

	stats := tracker.GetDailyStats()
	if stats.TradeCount != 4 {
		t.Fatalf("expected 4 trades, got %d", stats.TradeCount)
	}
	if stats.Wins != 1 {
		t.Fatalf("expected 1 win, got %d", stats.Wins)
	}
	if stats.Losses != 1 {
		t.Fatalf("expected 1 loss, got %d", stats.Losses)
	}
	if stats.WinRate != 50.0 {
		t.Fatalf("expected 50%% win rate, got %.1f", stats.WinRate)
	}
	if stats.LargestWin != 500 { // (150-100)*10
		t.Fatalf("expected largest win 500, got %d", stats.LargestWin)
	}
	if stats.LargestLoss != -100 { // (180-200)*5
		t.Fatalf("expected largest loss -100, got %d", stats.LargestLoss)
	}
}

func TestPnL_ResetDaily(t *testing.T) {
	tracker := NewPnLTracker()
	tracker.RecordTrade(Trade{Token: "A", Exchange: "NSE", Action: "BUY", Qty: 1, Price: 100, Timestamp: time.Now()})
	tracker.RecordTrade(Trade{Token: "A", Exchange: "NSE", Action: "SELL", Qty: 1, Price: 150, Timestamp: time.Now()})

	tracker.ResetDaily()
	stats := tracker.GetDailyStats()
	if stats.TradeCount != 0 || stats.Wins != 0 || stats.Losses != 0 {
		t.Fatalf("expected zeroed daily stats after reset, got %+v", stats)
	}
	// Total realized PnL should NOT be reset
	if tracker.GetRealizedPnL() != 50 {
		t.Fatalf("realized PnL should survive reset, got %d", tracker.GetRealizedPnL())
	}
}

func TestPnL_Snapshot_Restore(t *testing.T) {
	tracker := NewPnLTracker()
	tracker.RecordTrade(Trade{Token: "A", Exchange: "NSE", Action: "BUY", Qty: 10, Price: 100, Timestamp: time.Now()})
	tracker.RecordTrade(Trade{Token: "A", Exchange: "NSE", Action: "SELL", Qty: 10, Price: 120, Timestamp: time.Now()})

	snap, err := tracker.Snapshot()
	if err != nil {
		t.Fatalf("snapshot error: %v", err)
	}

	tracker2 := NewPnLTracker()
	if err := tracker2.RestorePnL(snap); err != nil {
		t.Fatalf("restore error: %v", err)
	}

	if tracker2.GetRealizedPnL() != tracker.GetRealizedPnL() {
		t.Fatalf("realized PnL mismatch: %d vs %d", tracker2.GetRealizedPnL(), tracker.GetRealizedPnL())
	}
	stats := tracker2.GetDailyStats()
	if stats.Wins != 1 {
		t.Fatalf("expected 1 win after restore, got %d", stats.Wins)
	}
}

// ══════════════════════════════════════════
// RiskManager tests
// ══════════════════════════════════════════

func TestRisk_CanTrade_MaxPositions(t *testing.T) {
	pf := New()
	limits := DefaultRiskLimits()
	limits.MaxOpenPositions = 2
	rm := NewRiskManager(limits, pf, 10000000)

	pf.OpenPosition("A", "NSE", 1, 100)
	pf.OpenPosition("B", "NSE", 1, 100)

	ok, reason := rm.CanTrade("C", "NSE", 1)
	if ok {
		t.Fatal("expected rejection for max positions")
	}
	if reason != "max open positions reached" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestRisk_CanTrade_PositionSize(t *testing.T) {
	pf := New()
	limits := DefaultRiskLimits()
	limits.MaxPositionSize = 10
	rm := NewRiskManager(limits, pf, 10000000)

	ok, reason := rm.CanTrade("A", "NSE", 20)
	if ok {
		t.Fatal("expected rejection for position size")
	}
	if reason != "position size exceeds limit" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestRisk_CanTrade_DailyLoss(t *testing.T) {
	pf := New()
	limits := DefaultRiskLimits()
	limits.MaxDailyLoss = 1000
	rm := NewRiskManager(limits, pf, 10000000)

	rm.RecordPnL(-2000) // exceed daily loss

	ok, reason := rm.CanTrade("A", "NSE", 1)
	if ok {
		t.Fatal("expected rejection for daily loss")
	}
	if reason != "max daily loss reached" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestRisk_CanTrade_Exposure(t *testing.T) {
	pf := New()
	limits := DefaultRiskLimits()
	limits.MaxExposure = 500 // very low
	rm := NewRiskManager(limits, pf, 10000000)

	pf.OpenPosition("A", "NSE", 10, 100) // exposure = 1000, exceeds 500

	ok, reason := rm.CanTrade("B", "NSE", 1)
	if ok {
		t.Fatal("expected rejection for exposure")
	}
	if reason != "max exposure reached" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestRisk_KillSwitch(t *testing.T) {
	pf := New()
	rm := NewRiskManager(DefaultRiskLimits(), pf, 10000000)

	rm.SetKillSwitch(true)
	if !rm.IsKillSwitchActive() {
		t.Fatal("expected kill switch active")
	}

	ok, reason := rm.CanTrade("A", "NSE", 1)
	if ok {
		t.Fatal("expected rejection due to kill switch")
	}
	if reason != "kill switch is active" {
		t.Fatalf("unexpected reason: %s", reason)
	}

	rm.SetKillSwitch(false)
	ok, _ = rm.CanTrade("A", "NSE", 1)
	if !ok {
		t.Fatal("expected trade allowed after kill switch deactivated")
	}
}

func TestRisk_Streaks(t *testing.T) {
	pf := New()
	rm := NewRiskManager(DefaultRiskLimits(), pf, 10000000)

	rm.RecordPnL(100)
	rm.RecordPnL(200)
	rm.RecordPnL(300)

	win, loss := rm.GetStreaks()
	if win != 3 {
		t.Fatalf("expected win streak 3, got %d", win)
	}
	if loss != 0 {
		t.Fatalf("expected loss streak 0, got %d", loss)
	}

	rm.RecordPnL(-50)
	rm.RecordPnL(-100)

	win, loss = rm.GetStreaks()
	if win != 0 {
		t.Fatalf("expected win streak reset to 0, got %d", win)
	}
	if loss != 2 {
		t.Fatalf("expected loss streak 2, got %d", loss)
	}
}

func TestRisk_Snapshot_Restore(t *testing.T) {
	pf := New()
	rm := NewRiskManager(DefaultRiskLimits(), pf, 10000000)
	rm.RecordPnL(500)
	rm.RecordPnL(-200)
	rm.SetKillSwitch(true)

	snap, err := rm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot error: %v", err)
	}

	rm2 := NewRiskManager(DefaultRiskLimits(), pf, 0) // equity will be restored
	if err := rm2.RestoreRisk(snap); err != nil {
		t.Fatalf("restore error: %v", err)
	}

	if !rm2.IsKillSwitchActive() {
		t.Fatal("expected kill switch active after restore")
	}
	status := rm2.GetStatus()
	if status["equity"].(int64) != 10000300 { // 10000000 + 500 - 200
		t.Fatalf("unexpected equity after restore: %v", status["equity"])
	}
}

func TestRisk_ResetDaily(t *testing.T) {
	pf := New()
	rm := NewRiskManager(DefaultRiskLimits(), pf, 10000000)
	rm.RecordPnL(-5000)

	rm.ResetDaily()
	status := rm.GetStatus()
	if status["daily_pnl"].(int64) != 0 {
		t.Fatalf("expected daily PnL 0 after reset, got %v", status["daily_pnl"])
	}
	// Equity should not reset
	if status["equity"].(int64) != 9995000 {
		t.Fatalf("equity should survive reset, got %v", status["equity"])
	}
}

// ══════════════════════════════════════════
// JSON round-trip sanity
// ══════════════════════════════════════════

func TestPosition_JSON(t *testing.T) {
	pos := Position{Token: "A", Exchange: "NSE", Qty: 10, AvgPrice: 100, LastLTP: 120}
	data, err := json.Marshal(pos)
	if err != nil {
		t.Fatal(err)
	}
	var pos2 Position
	if err := json.Unmarshal(data, &pos2); err != nil {
		t.Fatal(err)
	}
	if pos2 != pos {
		t.Fatalf("round-trip mismatch: %+v vs %+v", pos, pos2)
	}
}
