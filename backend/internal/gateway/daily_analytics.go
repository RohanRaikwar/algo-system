package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDailyWindow = 30
	maxDailyWindow     = 365
)

// DailyPnLItem is one day-level realized P&L summary row.
type DailyPnLItem struct {
	Date        string  `json:"date"`
	PnL         int64   `json:"pnl"`
	Trades      int     `json:"trades"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	WinRate     float64 `json:"win_rate"`
	LargestWin  int64   `json:"largest_win"`
	LargestLoss int64   `json:"largest_loss"`
}

// DailyPnLResponse is the API response shape for /api/pnl/daily.
type DailyPnLResponse struct {
	Days  int            `json:"days"`
	Items []DailyPnLItem `json:"items"`
	TS    string         `json:"ts"`
}

// DailyCompletedOrder is one completed BUY→EXIT order pair.
type DailyCompletedOrder struct {
	Strategy    string `json:"strategy"`
	Side        string `json:"side"`
	Exchange    string `json:"exchange"`
	Token       string `json:"token"`
	Instrument  string `json:"instrument"`
	Qty         int64  `json:"qty"`
	EntryPrice  int64  `json:"entry_price"`
	ExitPrice   int64  `json:"exit_price"`
	RealizedPnL int64  `json:"realized_pnl"`
	EntryTime   string `json:"entry_time"`
	ExitTime    string `json:"exit_time"`
}

// DailyOrdersGroup is one day bucket of completed orders.
type DailyOrdersGroup struct {
	Date   string                `json:"date"`
	PnL    int64                 `json:"pnl"`
	Trades int                   `json:"trades"`
	Orders []DailyCompletedOrder `json:"orders"`
}

// DailyOrdersResponse is the API response shape for /api/orders/daily.
type DailyOrdersResponse struct {
	Days  int                `json:"days"`
	Items []DailyOrdersGroup `json:"items"`
	TS    string             `json:"ts"`
}

// DailyAnalyticsService reads signal journals and builds day-wise analytics.
type DailyAnalyticsService struct {
	JournalPaths []string
	Location     *time.Location
}

// NewDailyAnalyticsService creates a new service.
func NewDailyAnalyticsService(journalPaths []string, loc *time.Location) *DailyAnalyticsService {
	if len(journalPaths) == 0 {
		journalPaths = resolveSignalJournalPaths()
	}
	if loc == nil {
		loc = mustLoadIST()
	}
	return &DailyAnalyticsService{
		JournalPaths: journalPaths,
		Location:     loc,
	}
}

type dailySignalRow struct {
	ID        int64
	Strategy  string
	Action    string
	Side      string
	Token     string
	Exchange  string
	Price     int64
	Qty       int64
	CandleTS  string
	CreatedAt string
	EventTime time.Time
}

type tradeKey struct {
	Strategy string
	Exchange string
	Token    string
	Side     string
}

type pairedTrade struct {
	Strategy   string
	Side       string
	Exchange   string
	Token      string
	Qty        int64
	EntryPrice int64
	ExitPrice  int64
	Realized   int64
	EntryAt    time.Time
	ExitAt     time.Time
}

// resolveSignalJournalPaths matches the same env-driven multi-journal pattern as /api/signals.
func resolveSignalJournalPaths() []string {
	journalPaths := []string{}
	if p := os.Getenv("STRAT_JOURNAL_PATH"); p != "" {
		journalPaths = append(journalPaths, p)
	}
	if p := os.Getenv("STRAT_IND_JOURNAL_PATH"); p != "" {
		journalPaths = append(journalPaths, p)
	}
	if len(journalPaths) == 0 {
		journalPaths = append(journalPaths, "data/signals.db")
	}
	return journalPaths
}

func mustLoadIST() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.FixedZone("IST", 5*3600+30*60)
	}
	return loc
}

func parseDailyDays(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultDailyWindow
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return defaultDailyWindow
	}
	if n > maxDailyWindow {
		return maxDailyWindow
	}
	return n
}

func normalizeQty(q int64) int64 {
	if q <= 0 {
		return 1
	}
	return q
}

func parseSignalTimestamp(createdAt, candleTS string) time.Time {
	if ts, ok := parseSignalTime(createdAt); ok {
		return ts
	}
	if ts, ok := parseSignalTime(candleTS); ok {
		return ts
	}
	return time.Time{}
}

func parseSignalTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}

	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), true
	}

	utcLayouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.999999999",
	}
	for _, layout := range utcLayouts {
		if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func queryDailySignalRows(db *sql.DB) (*sql.Rows, error) {
	withQty := `
		SELECT id, strategy, action, COALESCE(side,''), token, exchange, COALESCE(price,0),
		       CASE WHEN qty IS NULL OR qty <= 0 THEN 1 ELSE qty END,
		       COALESCE(candle_ts,''), COALESCE(created_at,'')
		FROM signals
		WHERE action IN ('BUY', 'EXIT')
		ORDER BY created_at ASC, id ASC`
	rows, err := db.Query(withQty)
	if err == nil {
		return rows, nil
	}

	// Legacy journals may not have qty yet.
	if strings.Contains(strings.ToLower(err.Error()), "no such column") &&
		strings.Contains(strings.ToLower(err.Error()), "qty") {
		legacy := `
			SELECT id, strategy, action, COALESCE(side,''), token, exchange, COALESCE(price,0),
			       1 AS qty,
			       COALESCE(candle_ts,''), COALESCE(created_at,'')
			FROM signals
			WHERE action IN ('BUY', 'EXIT')
			ORDER BY created_at ASC, id ASC`
		return db.Query(legacy)
	}
	return nil, err
}

func (svc *DailyAnalyticsService) loadSignalRows() ([]dailySignalRow, error) {
	rowsOut := make([]dailySignalRow, 0, 2048)
	for _, path := range svc.JournalPaths {
		db, err := openSignalDB(path)
		if err != nil {
			continue
		}

		rows, err := queryDailySignalRows(db)
		if err != nil {
			db.Close()
			continue
		}

		for rows.Next() {
			var r dailySignalRow
			if err := rows.Scan(
				&r.ID, &r.Strategy, &r.Action, &r.Side, &r.Token, &r.Exchange,
				&r.Price, &r.Qty, &r.CandleTS, &r.CreatedAt,
			); err != nil {
				continue
			}
			r.Action = strings.ToUpper(strings.TrimSpace(r.Action))
			r.Side = strings.ToUpper(strings.TrimSpace(r.Side))
			r.Qty = normalizeQty(r.Qty)
			r.EventTime = parseSignalTimestamp(r.CreatedAt, r.CandleTS)
			rowsOut = append(rowsOut, r)
		}
		rows.Close()
		db.Close()
	}

	sort.Slice(rowsOut, func(i, j int) bool {
		ti := rowsOut[i].EventTime
		tj := rowsOut[j].EventTime
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if rowsOut[i].CreatedAt != rowsOut[j].CreatedAt {
			return rowsOut[i].CreatedAt < rowsOut[j].CreatedAt
		}
		return rowsOut[i].ID < rowsOut[j].ID
	})

	return rowsOut, nil
}

func sameTradingDay(a, b time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = mustLoadIST()
	}
	dayA := a.In(loc).Format("2006-01-02")
	dayB := b.In(loc).Format("2006-01-02")
	return dayA == dayB
}

func pairCompletedTrades(rows []dailySignalRow) []pairedTrade {
	loc := mustLoadIST()
	pending := make(map[tradeKey][]dailySignalRow)
	completed := make([]pairedTrade, 0, len(rows)/2)

	var lastDay string // track current trading day to flush stale BUYs

	for _, row := range rows {
		// ── Flush stale pending BUYs at day boundaries ──
		// When we see a signal from a new trading day, discard all pending
		// BUYs from previous days. These are orphaned entries from service
		// restarts or sessions that were never properly exited.
		if !row.EventTime.IsZero() {
			rowDay := row.EventTime.In(loc).Format("2006-01-02")
			if lastDay != "" && rowDay != lastDay {
				for k, queue := range pending {
					var kept []dailySignalRow
					for _, p := range queue {
						if sameTradingDay(p.EventTime, row.EventTime, loc) {
							kept = append(kept, p)
						}
					}
					if len(kept) == 0 {
						delete(pending, k)
					} else {
						pending[k] = kept
					}
				}
			}
			lastDay = rowDay
		}

		key := tradeKey{
			Strategy: row.Strategy,
			Exchange: row.Exchange,
			Token:    row.Token,
			Side:     row.Side,
		}

		switch row.Action {
		case "BUY":
			pending[key] = append(pending[key], row)
		case "EXIT":
			// Skip EXIT signals with price=0 (EOD auto-exits without FNO price).
			// These create fake massive losses and corrupt P&L.
			if row.Price <= 0 {
				// Still consume the pending BUY so it doesn't pair later.
				queue := pending[key]
				if len(queue) > 0 {
					if len(queue) == 1 {
						delete(pending, key)
					} else {
						pending[key] = queue[1:]
					}
				}
				continue
			}

			queue := pending[key]
			if len(queue) == 0 {
				continue
			}
			entry := queue[0]
			if len(queue) == 1 {
				delete(pending, key)
			} else {
				pending[key] = queue[1:]
			}

			// Skip cross-day BUY→EXIT pairs. These are stale orphans
			// from service restarts — different FNO contracts/expiries.
			if !entry.EventTime.IsZero() && !row.EventTime.IsZero() &&
				!sameTradingDay(entry.EventTime, row.EventTime, loc) {
				continue
			}

			qty := normalizeQty(entry.Qty)
			if qty <= 0 {
				qty = normalizeQty(row.Qty)
			}
			realized := (row.Price - entry.Price) * qty
			completed = append(completed, pairedTrade{
				Strategy:   entry.Strategy,
				Side:       entry.Side,
				Exchange:   entry.Exchange,
				Token:      entry.Token,
				Qty:        qty,
				EntryPrice: entry.Price,
				ExitPrice:  row.Price,
				Realized:   realized,
				EntryAt:    entry.EventTime,
				ExitAt:     row.EventTime,
			})
		}
	}

	sort.Slice(completed, func(i, j int) bool {
		if !completed[i].ExitAt.Equal(completed[j].ExitAt) {
			return completed[i].ExitAt.After(completed[j].ExitAt)
		}
		if !completed[i].EntryAt.Equal(completed[j].EntryAt) {
			return completed[i].EntryAt.After(completed[j].EntryAt)
		}
		if completed[i].Strategy != completed[j].Strategy {
			return completed[i].Strategy < completed[j].Strategy
		}
		if completed[i].Exchange != completed[j].Exchange {
			return completed[i].Exchange < completed[j].Exchange
		}
		return completed[i].Token < completed[j].Token
	})

	return completed
}

func buildDailyPnLRows(trades []pairedTrade, loc *time.Location, days int) []DailyPnLItem {
	if loc == nil {
		loc = mustLoadIST()
	}
	type bucket struct {
		Date        string
		PnL         int64
		Trades      int
		Wins        int
		Losses      int
		LargestWin  int64
		LargestLoss int64
	}

	byDate := make(map[string]*bucket)
	for _, tr := range trades {
		date := tr.ExitAt.In(loc).Format("2006-01-02")
		b, ok := byDate[date]
		if !ok {
			b = &bucket{Date: date}
			byDate[date] = b
		}
		b.Trades++
		b.PnL += tr.Realized
		if tr.Realized > 0 {
			b.Wins++
			if tr.Realized > b.LargestWin {
				b.LargestWin = tr.Realized
			}
		} else if tr.Realized < 0 {
			b.Losses++
			if tr.Realized < b.LargestLoss {
				b.LargestLoss = tr.Realized
			}
		}
	}

	keys := make([]string, 0, len(byDate))
	for k := range byDate {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	if days > 0 && len(keys) > days {
		keys = keys[:days]
	}

	items := make([]DailyPnLItem, 0, len(keys))
	for _, k := range keys {
		b := byDate[k]
		winRate := 0.0
		if b.Wins+b.Losses > 0 {
			winRate = float64(b.Wins) / float64(b.Wins+b.Losses) * 100
		}
		items = append(items, DailyPnLItem{
			Date:        b.Date,
			PnL:         b.PnL,
			Trades:      b.Trades,
			Wins:        b.Wins,
			Losses:      b.Losses,
			WinRate:     winRate,
			LargestWin:  b.LargestWin,
			LargestLoss: b.LargestLoss,
		})
	}
	return items
}

func buildDailyOrderGroups(trades []pairedTrade, loc *time.Location, days int) []DailyOrdersGroup {
	if loc == nil {
		loc = mustLoadIST()
	}
	type bucket struct {
		Date   string
		PnL    int64
		Orders []pairedTrade
	}

	byDate := make(map[string]*bucket)
	for _, tr := range trades {
		date := tr.ExitAt.In(loc).Format("2006-01-02")
		b, ok := byDate[date]
		if !ok {
			b = &bucket{Date: date}
			byDate[date] = b
		}
		b.PnL += tr.Realized
		b.Orders = append(b.Orders, tr)
	}

	keys := make([]string, 0, len(byDate))
	for k := range byDate {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	if days > 0 && len(keys) > days {
		keys = keys[:days]
	}

	items := make([]DailyOrdersGroup, 0, len(keys))
	for _, k := range keys {
		b := byDate[k]
		sort.Slice(b.Orders, func(i, j int) bool {
			return b.Orders[i].ExitAt.After(b.Orders[j].ExitAt)
		})

		orders := make([]DailyCompletedOrder, 0, len(b.Orders))
		for _, tr := range b.Orders {
			orders = append(orders, DailyCompletedOrder{
				Strategy:    tr.Strategy,
				Side:        tr.Side,
				Exchange:    tr.Exchange,
				Token:       tr.Token,
				Instrument:  tr.Exchange + ":" + tr.Token,
				Qty:         tr.Qty,
				EntryPrice:  tr.EntryPrice,
				ExitPrice:   tr.ExitPrice,
				RealizedPnL: tr.Realized,
				EntryTime:   tr.EntryAt.UTC().Format(time.RFC3339Nano),
				ExitTime:    tr.ExitAt.UTC().Format(time.RFC3339Nano),
			})
		}
		items = append(items, DailyOrdersGroup{
			Date:   b.Date,
			PnL:    b.PnL,
			Trades: len(orders),
			Orders: orders,
		})
	}
	return items
}

// DailyPnL returns grouped day-wise realized P&L rows.
// If strategy is non-empty, only signals for that strategy are included.
func (svc *DailyAnalyticsService) DailyPnL(days int, strategy string) (DailyPnLResponse, error) {
	rows, err := svc.loadSignalRows()
	if err != nil {
		return DailyPnLResponse{}, err
	}
	if strategy != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Strategy == strategy {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	trades := pairCompletedTrades(rows)
	items := buildDailyPnLRows(trades, svc.Location, days)
	if items == nil {
		items = []DailyPnLItem{}
	}
	return DailyPnLResponse{
		Days:  days,
		Items: items,
		TS:    time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// DailyOrders returns grouped day-wise completed orders.
// If strategy is non-empty, only signals for that strategy are included.
func (svc *DailyAnalyticsService) DailyOrders(days int, strategy string) (DailyOrdersResponse, error) {
	rows, err := svc.loadSignalRows()
	if err != nil {
		return DailyOrdersResponse{}, err
	}
	if strategy != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Strategy == strategy {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	trades := pairCompletedTrades(rows)
	items := buildDailyOrderGroups(trades, svc.Location, days)
	if items == nil {
		items = []DailyOrdersGroup{}
	}
	return DailyOrdersResponse{
		Days:  days,
		Items: items,
		TS:    time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func makeDailyPnLHandler(fetch func(days int, strategy string) (DailyPnLResponse, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		days := parseDailyDays(r.URL.Query().Get("days"))
		strategy := r.URL.Query().Get("strategy")
		resp, err := fetch(days, strategy)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "failed to build daily pnl",
				"days":  days,
				"items": []DailyPnLItem{},
			})
			return
		}
		if resp.Items == nil {
			resp.Items = []DailyPnLItem{}
		}
		resp.Days = days
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func makeDailyOrdersHandler(fetch func(days int, strategy string) (DailyOrdersResponse, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		days := parseDailyDays(r.URL.Query().Get("days"))
		strategy := r.URL.Query().Get("strategy")
		resp, err := fetch(days, strategy)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "failed to build daily orders",
				"days":  days,
				"items": []DailyOrdersGroup{},
			})
			return
		}
		if resp.Items == nil {
			resp.Items = []DailyOrdersGroup{}
		}
		resp.Days = days
		_ = json.NewEncoder(w).Encode(resp)
	}
}
