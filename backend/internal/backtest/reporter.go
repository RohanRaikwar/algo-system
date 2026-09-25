package backtest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trading-systemv1/internal/strategy"
)

// ── Console Reporter ──────────────────────────────────────────────────

// PrintConsole renders the full backtest report to stdout.
func PrintConsole(r *Result) {
	if r.TotalCandles == 0 {
		fmt.Println("\n  No candle data found. Check your --db, --token, and --exchange flags.")
		return
	}

	printHeader(r)
	printTradeLog(r)
	printDaySummary(r)
	printEquityCurve(r)
	printMetrics(r)
}

func printHeader(r *Result) {
	from := "N/A"
	to := "N/A"
	if len(r.Trades) > 0 {
		from = r.Trades[0].EntryTime.In(istLoc).Format("2006-01-02 15:04")
		to = r.Trades[len(r.Trades)-1].ExitTime.In(istLoc).Format("2006-01-02 15:04")
	}

	stratName := r.Config.StrategyType
	if stratName == "" {
		stratName = "ema_1m"
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║          STRATEGY BACKTEST ENGINE                   ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Strategy:    %-38s  ║\n", stratName)
	fmt.Printf("║  Exchange:    %-38s  ║\n", r.Config.Exchange)
	fmt.Printf("║  Token:       %-38s  ║\n", r.Config.Token)
	fmt.Printf("║  Candles:     %-38d  ║\n", r.TotalCandles)
	fmt.Printf("║  First Trade: %-38s  ║\n", from)
	fmt.Printf("║  Last Trade:  %-38s  ║\n", to)
	fmt.Printf("║  Qty:         %-38d  ║\n", r.Config.Qty)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
}

func printTradeLog(r *Result) {
	if len(r.Trades) == 0 {
		fmt.Println("  No trades generated.")
		return
	}

	// Check if any trade has FNO data
	hasFNO := false
	for _, t := range r.Trades {
		if t.FNOEntryPrice > 0 || t.FNOExitPrice > 0 {
			hasFNO = true
			break
		}
	}

	if hasFNO {
		fmt.Println("  ┌─────┬──────┬──────────────────┬──────────────────┬────────────┬────────────┬──────────┬──────────┬────────────┬────────────────────────────────────┬────────────────────────────────────┐")
		fmt.Println("  │  #  │ Side │     Entry Time   │     Exit Time    │ Entry ₹    │ Exit ₹     │ FNO In   │ FNO Out  │ FNO P&L ₹  │ Entry Reason                       │ Exit Reason                        │")
		fmt.Println("  ├─────┼──────┼──────────────────┼──────────────────┼────────────┼────────────┼──────────┼──────────┼────────────┼────────────────────────────────────┼────────────────────────────────────┤")
	} else {
		fmt.Println("  ┌─────┬──────┬──────────────────┬──────────────────┬────────────┬────────────┬────────────┬────────────────────────────────────────────────────────┬────────────────────────────────────────────────────────┐")
		fmt.Println("  │  #  │ Side │     Entry Time   │     Exit Time    │ Entry ₹    │ Exit ₹     │ P&L ₹      │ Entry Reason                                           │ Exit Reason                                            │")
		fmt.Println("  ├─────┼──────┼──────────────────┼──────────────────┼────────────┼────────────┼────────────┼────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────┤")
	}

	for i, t := range r.Trades {
		pnl := t.PnLRupees() * float64(r.Config.Qty)
		pnlStr := fmt.Sprintf("%.2f", pnl)
		if pnl >= 0 {
			pnlStr = "+" + pnlStr
		}

		entryReason := t.EntryReason
		exitReason := t.ExitReason

		if hasFNO {
			if len(entryReason) > 34 {
				entryReason = entryReason[:31] + "..."
			}
			if len(exitReason) > 34 {
				exitReason = exitReason[:31] + "..."
			}

			fnoIn := fmt.Sprintf("%.2f", float64(t.FNOEntryPrice)/100.0)
			fnoOut := fmt.Sprintf("%.2f", float64(t.FNOExitPrice)/100.0)

			fmt.Printf("  │ %3d │ %-4s │ %s │ %s │ %10.2f │ %10.2f │ %8s │ %8s │ %10s │ %-34s │ %-34s │\n",
				i+1,
				string(t.Side),
				t.EntryTime.In(istLoc).Format("2006-01-02 15:04"),
				t.ExitTime.In(istLoc).Format("2006-01-02 15:04"),
				float64(t.EntryPrice)/100.0,
				float64(t.ExitPrice)/100.0,
				fnoIn,
				fnoOut,
				pnlStr,
				entryReason,
				exitReason,
			)
		} else {
			if len(entryReason) > 54 {
				entryReason = entryReason[:51] + "..."
			}
			if len(exitReason) > 54 {
				exitReason = exitReason[:51] + "..."
			}

			fmt.Printf("  │ %3d │ %-4s │ %s │ %s │ %10.2f │ %10.2f │ %10s │ %-54s │ %-54s │\n",
				i+1,
				string(t.Side),
				t.EntryTime.In(istLoc).Format("2006-01-02 15:04"),
				t.ExitTime.In(istLoc).Format("2006-01-02 15:04"),
				float64(t.EntryPrice)/100.0,
				float64(t.ExitPrice)/100.0,
				pnlStr,
				entryReason,
				exitReason,
			)
		}
	}

	if hasFNO {
		fmt.Println("  └─────┴──────┴──────────────────┴──────────────────┴────────────┴────────────┴──────────┴──────────┴────────────┴────────────────────────────────────┴────────────────────────────────────┘")
	} else {
		fmt.Println("  └─────┴──────┴──────────────────┴──────────────────┴────────────┴────────────┴────────────┴────────────────────────────────────────────────────────┴────────────────────────────────────────────────────────┘")
	}
	fmt.Println()
}

func printDaySummary(r *Result) {
	if len(r.DaySummaries) == 0 {
		return
	}

	fmt.Println("  ┌────────────┬────────┬──────┬────────┬────────────┬────────────┬────────────┐")
	fmt.Println("  │    Date    │ Trades │ Wins │ Losses │ Day P&L ₹  │ Cum P&L ₹  │ Drawdown ₹ │")
	fmt.Println("  ├────────────┼────────┼──────┼────────┼────────────┼────────────┼────────────┤")

	for _, d := range r.DaySummaries {
		dayPnL := formatPnLSigned(d.PnL)
		cumPnL := formatPnLSigned(d.CumPnL)
		dd := fmt.Sprintf("%.2f", d.MaxDrawdown)

		fmt.Printf("  │ %s │ %6d │ %4d │ %6d │ %10s │ %10s │ %10s │\n",
			d.Date, d.Trades, d.Wins, d.Losses, dayPnL, cumPnL, dd)
	}

	fmt.Println("  └────────────┴────────┴──────┴────────┴────────────┴────────────┴────────────┘")
	fmt.Println()
}

func printEquityCurve(r *Result) {
	if len(r.EquityCurve) == 0 {
		return
	}

	fmt.Println("  📈 Equity Curve (cumulative P&L after each trade):")
	fmt.Print("  ")

	// ASCII mini sparkline
	minVal := r.EquityCurve[0]
	maxVal := r.EquityCurve[0]
	for _, v := range r.EquityCurve {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	spread := maxVal - minVal
	if spread == 0 {
		spread = 1
	}

	for _, v := range r.EquityCurve {
		idx := int((v - minVal) / spread * float64(len(bars)-1))
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		if idx < 0 {
			idx = 0
		}
		fmt.Printf("%c", bars[idx])
	}

	fmt.Printf("  (min=%.2f, max=%.2f)\n\n", minVal, maxVal)
}

func printMetrics(r *Result) {
	m := r.Metrics

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║          BACKTEST RESULTS                           ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Candles:     %-32d  ║\n", r.TotalCandles)
	fmt.Printf("║  Trading Days:      %-32d  ║\n", m.TotalDays)
	fmt.Printf("║  ──────────────────────────────────────────────────  ║\n")
	fmt.Printf("║  Total Trades:      %-32d  ║\n", m.TotalTrades)
	fmt.Printf("║  CALL Trades:       %-32d  ║\n", m.CallTrades)
	fmt.Printf("║  PUT Trades:        %-32d  ║\n", m.PutTrades)
	fmt.Printf("║  Wins:              %-32d  ║\n", m.Wins)
	fmt.Printf("║  Losses:            %-32d  ║\n", m.Losses)
	fmt.Printf("║  Win Rate:          %-32s  ║\n", fmt.Sprintf("%.1f%%", m.WinRate))
	fmt.Printf("║  Avg Duration:      %-32s  ║\n", m.AvgDuration)
	fmt.Printf("║  ──────────────────────────────────────────────────  ║\n")
	fmt.Printf("║  Net P&L:           %-32s  ║\n", formatPnL(m.NetPnL))
	fmt.Printf("║  Gross Profit:      %-32s  ║\n", formatPnL(m.GrossProfit))
	fmt.Printf("║  Gross Loss:        %-32s  ║\n", formatPnL(m.GrossLoss))
	fmt.Printf("║  Profit Factor:     %-32s  ║\n", fmt.Sprintf("%.2f", m.ProfitFactor))
	fmt.Printf("║  ──────────────────────────────────────────────────  ║\n")
	fmt.Printf("║  Best Trade:        %-32s  ║\n", formatPnL(m.BestTrade))
	fmt.Printf("║  Worst Trade:       %-32s  ║\n", formatPnL(m.WorstTrade))
	fmt.Printf("║  Avg Win:           %-32s  ║\n", formatPnL(m.AvgWin))
	fmt.Printf("║  Avg Loss:          %-32s  ║\n", formatPnL(m.AvgLoss))
	fmt.Printf("║  ──────────────────────────────────────────────────  ║\n")
	fmt.Printf("║  Max Drawdown:      %-32s  ║\n", formatPnL(m.MaxDrawdown))
	fmt.Printf("║  Sharpe Ratio:      %-32s  ║\n", fmt.Sprintf("%.2f", m.SharpeRatio))
	fmt.Println("╚══════════════════════════════════════════════════════╝")
}

// ── JSON Export ───────────────────────────────────────────────────────

// WriteJSON exports the full result as a JSON file.
func WriteJSON(r *Result, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(outDir, "backtest_result.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}

	fmt.Printf("\n  ✅ JSON exported to %s\n", path)
	return nil
}

// ── CSV Export ────────────────────────────────────────────────────────

// WriteCSV exports trades and daily summaries as CSV files.
func WriteCSV(r *Result, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// ── trades.csv ──
	tradesPath := filepath.Join(outDir, "trades.csv")
	tf, err := os.Create(tradesPath)
	if err != nil {
		return err
	}
	defer tf.Close()

	tw := csv.NewWriter(tf)
	tw.Write([]string{"#", "Side", "Entry Time", "Exit Time", "Entry Price", "Exit Price", "FNO Entry", "FNO Exit", "P&L", "Duration", "Entry Reason", "Exit Reason"})
	for i, t := range r.Trades {
		pnl := t.PnLRupees() * float64(r.Config.Qty)
		tw.Write([]string{
			fmt.Sprintf("%d", i+1),
			string(t.Side),
			t.EntryTime.In(istLoc).Format(time.RFC3339),
			t.ExitTime.In(istLoc).Format(time.RFC3339),
			fmt.Sprintf("%.2f", float64(t.EntryPrice)/100.0),
			fmt.Sprintf("%.2f", float64(t.ExitPrice)/100.0),
			fmt.Sprintf("%.2f", float64(t.FNOEntryPrice)/100.0),
			fmt.Sprintf("%.2f", float64(t.FNOExitPrice)/100.0),
			fmt.Sprintf("%.2f", pnl),
			t.Duration().String(),
			t.EntryReason,
			t.ExitReason,
		})
	}
	tw.Flush()

	// ── daily_summary.csv ──
	dailyPath := filepath.Join(outDir, "daily_summary.csv")
	df, err := os.Create(dailyPath)
	if err != nil {
		return err
	}
	defer df.Close()

	dw := csv.NewWriter(df)
	dw.Write([]string{"Date", "Trades", "Wins", "Losses", "Day P&L", "Cumulative P&L", "Max Drawdown"})
	for _, d := range r.DaySummaries {
		dw.Write([]string{
			d.Date,
			fmt.Sprintf("%d", d.Trades),
			fmt.Sprintf("%d", d.Wins),
			fmt.Sprintf("%d", d.Losses),
			fmt.Sprintf("%.2f", d.PnL),
			fmt.Sprintf("%.2f", d.CumPnL),
			fmt.Sprintf("%.2f", d.MaxDrawdown),
		})
	}
	dw.Flush()

	fmt.Printf("\n  ✅ CSV exported:\n")
	fmt.Printf("     trades:        %s\n", tradesPath)
	fmt.Printf("     daily summary: %s\n", dailyPath)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────

func formatPnL(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("+₹%.2f", v)
	}
	return fmt.Sprintf("-₹%.2f", -v)
}

func formatPnLSigned(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("+%.2f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func pnlIcon(v float64) string {
	if v > 0 {
		return "🟢"
	}
	if v < 0 {
		return "🔴"
	}
	return "⚪"
}

// sideSummary returns a one-line summary for a given side.
func sideSummary(trades []Trade, side strategy.PositionSide, qty int64) string {
	var count, wins int
	var pnl float64
	for _, t := range trades {
		if t.Side != side {
			continue
		}
		count++
		p := t.PnLRupees() * float64(qty)
		pnl += p
		if p >= 0 {
			wins++
		}
	}
	if count == 0 {
		return "0 trades"
	}
	wr := float64(wins) / float64(count) * 100
	return fmt.Sprintf("%d trades, %.1f%% win, %s", count, wr, formatPnL(pnl))
}
