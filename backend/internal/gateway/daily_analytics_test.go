package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPairCompletedTrades_Interleaved(t *testing.T) {
	rows := []dailySignalRow{
		{
			ID: 1, Strategy: "S1", Action: "BUY", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 100, Qty: 2, EventTime: time.Date(2026, 3, 20, 9, 16, 0, 0, time.UTC),
		},
		{
			ID: 2, Strategy: "S2", Action: "BUY", Side: "PUT", Exchange: "NFO", Token: "222",
			Price: 200, Qty: 1, EventTime: time.Date(2026, 3, 20, 9, 17, 0, 0, time.UTC),
		},
		{
			ID: 3, Strategy: "S1", Action: "EXIT", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 110, Qty: 2, EventTime: time.Date(2026, 3, 20, 9, 18, 0, 0, time.UTC),
		},
		{
			ID: 4, Strategy: "S2", Action: "EXIT", Side: "PUT", Exchange: "NFO", Token: "222",
			Price: 180, Qty: 1, EventTime: time.Date(2026, 3, 20, 9, 19, 0, 0, time.UTC),
		},
	}

	trades := pairCompletedTrades(rows)
	if len(trades) != 2 {
		t.Fatalf("expected 2 completed trades, got %d", len(trades))
	}

	// Trades are sorted by exit time descending.
	if trades[0].Strategy != "S2" || trades[0].Realized != -20 {
		t.Fatalf("unexpected first trade: %+v", trades[0])
	}
	if trades[1].Strategy != "S1" || trades[1].Realized != 20 {
		t.Fatalf("unexpected second trade: %+v", trades[1])
	}
}

func TestPairCompletedTrades_LegacyQtyFallback(t *testing.T) {
	rows := []dailySignalRow{
		{
			ID: 1, Strategy: "S1", Action: "BUY", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 100, Qty: 0, EventTime: time.Date(2026, 3, 20, 9, 16, 0, 0, time.UTC),
		},
		{
			ID: 2, Strategy: "S1", Action: "EXIT", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 105, Qty: 0, EventTime: time.Date(2026, 3, 20, 9, 18, 0, 0, time.UTC),
		},
	}
	trades := pairCompletedTrades(rows)
	if len(trades) != 1 {
		t.Fatalf("expected 1 completed trade, got %d", len(trades))
	}
	if trades[0].Qty != 1 {
		t.Fatalf("expected fallback qty=1, got %d", trades[0].Qty)
	}
	if trades[0].Realized != 5 {
		t.Fatalf("expected realized pnl=5, got %d", trades[0].Realized)
	}
}

func TestBuildDailyPnLRows_ISTBoundary(t *testing.T) {
	ist := mustLoadIST()
	trades := []pairedTrade{
		{
			Strategy: "S1", Side: "CALL", Exchange: "NFO", Token: "111",
			Qty: 1, EntryPrice: 100, ExitPrice: 200, Realized: 100,
			EntryAt: time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC),
			ExitAt:  time.Date(2026, 3, 1, 18, 20, 0, 0, time.UTC), // 2026-03-01 23:50 IST
		},
		{
			Strategy: "S1", Side: "CALL", Exchange: "NFO", Token: "111",
			Qty: 1, EntryPrice: 100, ExitPrice: 50, Realized: -50,
			EntryAt: time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC),
			ExitAt:  time.Date(2026, 3, 1, 18, 40, 0, 0, time.UTC), // 2026-03-02 00:10 IST
		},
	}

	rows := buildDailyPnLRows(trades, ist, 30)
	if len(rows) != 2 {
		t.Fatalf("expected 2 daily rows, got %d", len(rows))
	}
	if rows[0].Date != "2026-03-02" || rows[0].PnL != -50 {
		t.Fatalf("unexpected first day row: %+v", rows[0])
	}
	if rows[1].Date != "2026-03-01" || rows[1].PnL != 100 {
		t.Fatalf("unexpected second day row: %+v", rows[1])
	}
}

func TestMakeDailyPnLHandler_DaysDefaultAndInvalid(t *testing.T) {
	calls := []int{}
	handler := makeDailyPnLHandler(func(days int, strategy string) (DailyPnLResponse, error) {
		calls = append(calls, days)
		return DailyPnLResponse{Days: days, Items: []DailyPnLItem{}}, nil
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/pnl/daily", nil)
	handler(rr, req)

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/pnl/daily?days=bad", nil)
	handler(rr2, req2)

	if len(calls) != 2 {
		t.Fatalf("expected 2 handler calls, got %d", len(calls))
	}
	if calls[0] != defaultDailyWindow {
		t.Fatalf("expected default days=%d, got %d", defaultDailyWindow, calls[0])
	}
	if calls[1] != defaultDailyWindow {
		t.Fatalf("expected invalid days fallback=%d, got %d", defaultDailyWindow, calls[1])
	}
}

func TestMakeDailyOrdersHandler_DaysDefaultAndInvalid(t *testing.T) {
	calls := []int{}
	handler := makeDailyOrdersHandler(func(days int, strategy string) (DailyOrdersResponse, error) {
		calls = append(calls, days)
		return DailyOrdersResponse{Days: days, Items: []DailyOrdersGroup{}}, nil
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/orders/daily?days=-10", nil)
	handler(rr, req)

	if len(calls) != 1 {
		t.Fatalf("expected 1 handler call, got %d", len(calls))
	}
	if calls[0] != defaultDailyWindow {
		t.Fatalf("expected invalid days fallback=%d, got %d", defaultDailyWindow, calls[0])
	}

	var payload DailyOrdersResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if payload.Days != defaultDailyWindow {
		t.Fatalf("expected response days=%d, got %d", defaultDailyWindow, payload.Days)
	}
}

func TestPairCompletedTrades_SkipZeroPriceExit(t *testing.T) {
	rows := []dailySignalRow{
		{
			ID: 1, Strategy: "S1", Action: "BUY", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 100, Qty: 1, EventTime: time.Date(2026, 4, 16, 9, 0, 0, 0, time.UTC),
		},
		{
			// EOD auto-exit with price=0 — should be skipped, not create a fake -100 loss
			ID: 2, Strategy: "S1", Action: "EXIT", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 0, Qty: 1, EventTime: time.Date(2026, 4, 16, 15, 30, 0, 0, time.UTC),
		},
	}
	trades := pairCompletedTrades(rows)
	if len(trades) != 0 {
		t.Fatalf("expected 0 trades (zero-price EXIT should be skipped), got %d: %+v", len(trades), trades)
	}
}

func TestPairCompletedTrades_CrossDaySkipped(t *testing.T) {
	rows := []dailySignalRow{
		{
			// BUY from April 3 — never properly exited
			ID: 1, Strategy: "S1", Action: "BUY", Side: "PUT", Exchange: "NFO", Token: "111",
			Price: 45000, Qty: 1, EventTime: time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC),
		},
		{
			// Normal BUY on April 15
			ID: 2, Strategy: "S1", Action: "BUY", Side: "PUT", Exchange: "NFO", Token: "111",
			Price: 21000, Qty: 1, EventTime: time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC),
		},
		{
			// EXIT on April 15 — should pair with BUY#2, not BUY#1
			ID: 3, Strategy: "S1", Action: "EXIT", Side: "PUT", Exchange: "NFO", Token: "111",
			Price: 21500, Qty: 1, EventTime: time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		},
	}
	trades := pairCompletedTrades(rows)
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d: %+v", len(trades), trades)
	}
	// Should pair with the same-day BUY at 21000, not the stale April 3 BUY at 45000
	if trades[0].EntryPrice != 21000 {
		t.Fatalf("expected entry_price=21000 (same-day BUY), got %d", trades[0].EntryPrice)
	}
	if trades[0].Realized != 500 {
		t.Fatalf("expected realized=500, got %d", trades[0].Realized)
	}
}

func TestPairCompletedTrades_FlushStaleBuysAtDayBoundary(t *testing.T) {
	rows := []dailySignalRow{
		{
			// Stale BUY from previous day — should be flushed when day changes
			ID: 1, Strategy: "S1", Action: "BUY", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 25000, Qty: 1, EventTime: time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC),
		},
		{
			// New BUY on new day
			ID: 2, Strategy: "S1", Action: "BUY", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 13000, Qty: 1, EventTime: time.Date(2026, 4, 16, 9, 0, 0, 0, time.UTC),
		},
		{
			// EXIT on same new day
			ID: 3, Strategy: "S1", Action: "EXIT", Side: "CALL", Exchange: "NFO", Token: "111",
			Price: 13500, Qty: 1, EventTime: time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC),
		},
	}
	trades := pairCompletedTrades(rows)
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade (stale BUY flushed), got %d: %+v", len(trades), trades)
	}
	// The stale BUY from April 3 should be flushed at day boundary.
	// EXIT should pair with the April 16 BUY.
	if trades[0].EntryPrice != 13000 {
		t.Fatalf("expected entry_price=13000 (same-day BUY), got %d", trades[0].EntryPrice)
	}
	if trades[0].Realized != 500 {
		t.Fatalf("expected realized=500, got %d", trades[0].Realized)
	}
}

