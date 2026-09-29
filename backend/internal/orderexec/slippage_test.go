package orderexec

import (
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

func TestPaperSlippage(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]", PaperSlippageBps: 50, PaperSlippageMinPaise: 50})
	// 0.5% of ₹100 = ₹0.50; min ₹0.50 → 50 paise.
	if got := oe.paperFillPrice("BUY", 10000); got != 10050 {
		t.Fatalf("buy fill = %d", got)
	}
	if got := oe.paperFillPrice("SELL", 10000); got != 9950 {
		t.Fatalf("sell fill = %d", got)
	}
	// Cheap option: 0.5% of ₹4 = 2 paise → min 50 paise applies.
	if got := oe.paperFillPrice("BUY", 400); got != 450 {
		t.Fatalf("min slippage buy = %d", got)
	}
	// A sell never fills at or below zero.
	if got := oe.paperFillPrice("SELL", 30); got != 5 {
		t.Fatalf("floor sell = %d", got)
	}
	if got := oe.paperFillPrice("BUY", 0); got != 0 {
		t.Fatal("no price stays no price")
	}
	off := NewOrderExecutor(Config{Qty: 1, LogPrefix: "[test]"})
	if got := off.paperFillPrice("BUY", 10000); got != 10000 {
		t.Fatal("zero config must not slip")
	}
}

func TestPaperSlippageAppliedToFills(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]", PaperSlippageBps: 100,
		CallFNOToken: "111", CallFNOSymbol: "CE"})
	fills := captureFills(oe)
	setLTP(oe, "111", 10000)
	oe.ExecuteSignal(strategy.Signal{StrategyName: "NIFTY50_RANGE", Action: strategy.ActionBuy, Side: strategy.SideCall, Token: "99926000", Exchange: "NSE"})
	oe.ExecuteSignal(strategy.Signal{StrategyName: "NIFTY50_RANGE", Action: strategy.ActionExit, Side: strategy.SideCall, Token: "99926000", Exchange: "NSE"})
	short := legSig(strategy.ActionBuy, "SHORT_CE", "302", strategy.SideCall, true)
	setLTP(oe, "302", 10000)
	oe.ExecuteSignal(short)
	got := fills.get()
	if len(got) != 3 || got[0].FillPricePaise != 10100 || got[1].FillPricePaise != 9900 {
		t.Fatalf("single-leg fills = %+v", got)
	}
	if got[2].Direction != "SELL" || got[2].FillPricePaise != 9900 {
		t.Fatalf("short leg open should sell at ltp−slip: %+v", got[2])
	}
}

type fixedQuotes struct {
	bid, ask int64
	ok       bool
}

func (f fixedQuotes) BidAsk(string, time.Time) (int64, int64, bool) { return f.bid, f.ask, f.ok }

func TestPaperFillUsesQuotedAskBid(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]", PaperSlippageBps: 50, PaperSlippageMinPaise: 5})
	oe.SetQuoteSource(fixedQuotes{bid: 20280, ask: 20320, ok: true})
	if got := oe.paperFill("BUY", "40712", 20300); got != 20320 {
		t.Fatalf("buy fill %d, want ask 20320", got)
	}
	if got := oe.paperFill("SELL", "40712", 20300); got != 20280 {
		t.Fatalf("sell fill %d, want bid 20280", got)
	}
	oe.SetQuoteSource(fixedQuotes{ok: false}) // stale/no quote → LTP ± slippage
	if got := oe.paperFill("BUY", "40712", 10000); got != 10050 {
		t.Fatalf("fallback buy fill %d, want 10050", got)
	}
}
