// Quick analysis tool for backtest results
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

type Trade struct {
	ID          int       `json:"id"`
	Side        string    `json:"side"`
	EntryTime   time.Time `json:"entry_time"`
	ExitTime    time.Time `json:"exit_time"`
	EntryPrice  int64     `json:"entry_price"`
	ExitPrice   int64     `json:"exit_price"`
	EntryReason string    `json:"entry_reason"`
	ExitReason  string    `json:"exit_reason"`
}

func (t Trade) PnL() float64 {
	switch t.Side {
	case "CALL":
		return float64(t.ExitPrice-t.EntryPrice) / 100
	case "PUT":
		return float64(t.EntryPrice-t.ExitPrice) / 100
	default:
		return 0
	}
}

func (t Trade) Duration() time.Duration {
	return t.ExitTime.Sub(t.EntryTime)
}

type DaySummary struct {
	Date   string  `json:"date"`
	Trades int     `json:"trades"`
	Wins   int     `json:"wins"`
	Losses int     `json:"losses"`
	PnL    float64 `json:"pnl"`
}

type Result struct {
	Trades       []Trade      `json:"trades"`
	DaySummaries []DaySummary `json:"day_summaries"`
}

func main() {
	data, err := os.ReadFile("data/backtest_result.json")
	if err != nil {
		panic(err)
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}

	trades := result.Trades
	days := result.DaySummaries

	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Println("  DEEP ANALYSIS — 6-Month Backtest")
	fmt.Println("═══════════════════════════════════════════════════")

	// ── 1. Entry type breakdown ──
	fmt.Println("\n── 1. ENTRY TYPE BREAKDOWN ──")
	entryTypes := map[string]struct {
		count, wins int
		pnl         float64
	}{}
	for _, t := range trades {
		key := categorizeEntry(t.EntryReason)
		e := entryTypes[key]
		e.count++
		pnl := t.PnL()
		e.pnl += pnl
		if pnl >= 0 {
			e.wins++
		}
		entryTypes[key] = e
	}
	fmt.Printf("  %-20s %6s %6s %10s %8s\n", "Entry Type", "Count", "WinR%", "Net P&L", "Avg P&L")
	for k, v := range entryTypes {
		wr := float64(v.wins) / float64(v.count) * 100
		avg := v.pnl / float64(v.count)
		fmt.Printf("  %-20s %6d %5.1f%% %+9.2f %+7.2f\n", k, v.count, wr, v.pnl, avg)
	}

	// ── 2. Exit type breakdown ──
	fmt.Println("\n── 2. EXIT TYPE BREAKDOWN ──")
	exitTypes := map[string]struct {
		count int
		pnl   float64
	}{}
	for _, t := range trades {
		key := categorizeExit(t.ExitReason)
		e := exitTypes[key]
		e.count++
		e.pnl += t.PnL()
		exitTypes[key] = e
	}
	fmt.Printf("  %-25s %6s %10s %8s\n", "Exit Type", "Count", "Net P&L", "Avg P&L")
	for k, v := range exitTypes {
		avg := v.pnl / float64(v.count)
		fmt.Printf("  %-25s %6d %+9.2f %+7.2f\n", k, v.count, v.pnl, avg)
	}

	// ── 3. Time of day analysis ──
	fmt.Println("\n── 3. TIME OF DAY ANALYSIS (hourly) ──")
	ist, _ := time.LoadLocation("Asia/Kolkata")
	hourStats := map[int]struct {
		count, wins int
		pnl         float64
	}{}
	for _, t := range trades {
		h := t.EntryTime.In(ist).Hour()
		e := hourStats[h]
		e.count++
		pnl := t.PnL()
		e.pnl += pnl
		if pnl >= 0 {
			e.wins++
		}
		hourStats[h] = e
	}
	hours := make([]int, 0)
	for h := range hourStats {
		hours = append(hours, h)
	}
	sort.Ints(hours)
	fmt.Printf("  %5s %6s %6s %10s %8s\n", "Hour", "Count", "WinR%", "Net P&L", "Avg P&L")
	for _, h := range hours {
		v := hourStats[h]
		wr := float64(v.wins) / float64(v.count) * 100
		avg := v.pnl / float64(v.count)
		fmt.Printf("  %02d:00 %6d %5.1f%% %+9.2f %+7.2f\n", h, v.count, wr, v.pnl, avg)
	}

	// ── 4. Worst and best days ──
	fmt.Println("\n── 4. TOP 10 WORST DAYS ──")
	sort.Slice(days, func(i, j int) bool { return days[i].PnL < days[j].PnL })
	fmt.Printf("  %-12s %6s %6s %6s %10s\n", "Date", "Trades", "Wins", "Losses", "P&L")
	for i := 0; i < 10 && i < len(days); i++ {
		d := days[i]
		fmt.Printf("  %-12s %6d %6d %6d %+9.2f\n", d.Date, d.Trades, d.Wins, d.Losses, d.PnL)
	}

	fmt.Println("\n── 5. TOP 10 BEST DAYS ──")
	sort.Slice(days, func(i, j int) bool { return days[i].PnL > days[j].PnL })
	fmt.Printf("  %-12s %6s %6s %6s %10s\n", "Date", "Trades", "Wins", "Losses", "P&L")
	for i := 0; i < 10 && i < len(days); i++ {
		d := days[i]
		fmt.Printf("  %-12s %6d %6d %6d %+9.2f\n", d.Date, d.Trades, d.Wins, d.Losses, d.PnL)
	}

	// ── 5b. Trade count distribution ──
	fmt.Println("\n── 6. TRADE COUNT VS PROFITABILITY ──")
	tcBuckets := map[string]struct {
		count int
		pnl   float64
		wins  int
	}{}
	for _, d := range days {
		var bucket string
		switch {
		case d.Trades <= 2:
			bucket = "1-2 trades"
		case d.Trades <= 4:
			bucket = "3-4 trades"
		case d.Trades <= 6:
			bucket = "5-6 trades"
		case d.Trades <= 8:
			bucket = "7-8 trades"
		default:
			bucket = "9+ trades"
		}
		b := tcBuckets[bucket]
		b.count++
		b.pnl += d.PnL
		if d.PnL >= 0 {
			b.wins++
		}
		tcBuckets[bucket] = b
	}
	bucketOrder := []string{"1-2 trades", "3-4 trades", "5-6 trades", "7-8 trades", "9+ trades"}
	fmt.Printf("  %-12s %6s %6s %10s %8s\n", "Bucket", "Days", "WinD%", "Net P&L", "Avg/Day")
	for _, b := range bucketOrder {
		v := tcBuckets[b]
		if v.count == 0 {
			continue
		}
		wr := float64(v.wins) / float64(v.count) * 100
		avg := v.pnl / float64(v.count)
		fmt.Printf("  %-12s %6d %5.1f%% %+9.2f %+7.2f\n", b, v.count, wr, v.pnl, avg)
	}

	// ── 7. Consecutive loss streaks ──
	fmt.Println("\n── 7. CONSECUTIVE LOSS STREAKS ──")
	maxStreak := 0
	curStreak := 0
	streakPnL := 0.0
	maxStreakPnL := 0.0
	for _, t := range trades {
		if t.PnL() < 0 {
			curStreak++
			streakPnL += t.PnL()
			if curStreak > maxStreak {
				maxStreak = curStreak
				maxStreakPnL = streakPnL
			}
		} else {
			curStreak = 0
			streakPnL = 0
		}
	}
	fmt.Printf("  Max consecutive losses: %d (total P&L: %+.2f)\n", maxStreak, maxStreakPnL)

	// ── 8. Win rate by trade duration ──
	fmt.Println("\n── 8. P&L BY TRADE DURATION ──")
	durBuckets := map[string]struct {
		count, wins int
		pnl         float64
	}{}
	for _, t := range trades {
		d := t.Duration()
		var bucket string
		switch {
		case d <= 5*time.Minute:
			bucket = "0-5m"
		case d <= 10*time.Minute:
			bucket = "5-10m"
		case d <= 20*time.Minute:
			bucket = "10-20m"
		case d <= 30*time.Minute:
			bucket = "20-30m"
		default:
			bucket = "30m+"
		}
		b := durBuckets[bucket]
		b.count++
		pnl := t.PnL()
		b.pnl += pnl
		if pnl >= 0 {
			b.wins++
		}
		durBuckets[bucket] = b
	}
	durOrder := []string{"0-5m", "5-10m", "10-20m", "20-30m", "30m+"}
	fmt.Printf("  %-8s %6s %6s %10s %8s\n", "Duration", "Count", "WinR%", "Net P&L", "Avg P&L")
	for _, b := range durOrder {
		v := durBuckets[b]
		if v.count == 0 {
			continue
		}
		wr := float64(v.wins) / float64(v.count) * 100
		avg := v.pnl / float64(v.count)
		fmt.Printf("  %-8s %6d %5.1f%% %+9.2f %+7.2f\n", b, v.count, wr, v.pnl, avg)
	}

	// ── 9. SL hit analysis ──
	fmt.Println("\n── 9. STOP LOSS HIT ANALYSIS ──")
	slCount := 0
	slPnL := 0.0
	nonSLCount := 0
	nonSLPnL := 0.0
	for _, t := range trades {
		if strings.Contains(t.ExitReason, "HARD SL") {
			slCount++
			slPnL += t.PnL()
		} else {
			nonSLCount++
			nonSLPnL += t.PnL()
		}
	}
	fmt.Printf("  SL exits:     %d trades, P&L: %+.2f (avg: %+.2f)\n", slCount, slPnL, slPnL/math.Max(float64(slCount), 1))
	fmt.Printf("  Non-SL exits: %d trades, P&L: %+.2f (avg: %+.2f)\n", nonSLCount, nonSLPnL, nonSLPnL/math.Max(float64(nonSLCount), 1))

	// ── 10. CALL vs PUT breakdown ──
	fmt.Println("\n── 10. CALL vs PUT PERFORMANCE ──")
	for _, side := range []string{"CALL", "PUT"} {
		var count, wins int
		var pnl float64
		for _, t := range trades {
			if t.Side == side {
				count++
				p := t.PnL()
				pnl += p
				if p >= 0 {
					wins++
				}
			}
		}
		wr := float64(wins) / float64(count) * 100
		avg := pnl / float64(count)
		fmt.Printf("  %s: %d trades, WR=%.1f%%, P&L=%+.2f, Avg=%+.2f\n", side, count, wr, pnl, avg)
	}
}

func categorizeEntry(reason string) string {
	switch {
	case strings.Contains(reason, "momentum"):
		return "Momentum Bypass"
	case strings.Contains(reason, "re-entry"):
		return "Re-entry (EMA6×EMA9)"
	case strings.Contains(reason, "pending"):
		return "Pending (cooldown)"
	default:
		return "Initial Entry"
	}
}

func categorizeExit(reason string) string {
	switch {
	case strings.Contains(reason, "HARD SL"):
		return "Hard Stop Loss"
	case strings.Contains(reason, "replaced"):
		return "Replaced by new entry"
	case strings.Contains(reason, "15:29"):
		return "EOD (15:29 close)"
	case strings.Contains(reason, "end of day"):
		return "EOD (forced close)"
	case strings.Contains(reason, "exit"):
		return "EMA crossover exit"
	default:
		return "Other"
	}
}
