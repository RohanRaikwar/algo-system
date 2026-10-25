package stratengine

import (
	"context"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func tgService(r *offlineFake) (*Service, *[]string) {
	var subs []string
	svc := &Service{
		legResolver:   r,
		orderExecutor: orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"}),
		cfg:           Config{EODExitTime: "15:15", TGArmMin: 30, TGMaxStrikeGapPct: 3, TGMaxPriceGapPct: 40},
		tgStrategy:    strategy.NewTelegramSignal(65, strategy.TelegramSignalConfig{TargetPct: 40, MaxChasePct: 5}),
	}
	svc.subscribeHook = func(tokens []string) { subs = append(subs, tokens...) }
	return svc, &subs
}

func tgCall(now time.Time) model.TGSignal {
	return model.TGSignal{ChatID: 1, MsgID: 2, Underlying: "NIFTY", Strike: 24500, OptionType: "CE",
		TriggerPaise: 12000, SLPaise: 10000, ReceivedAt: now}
}

func TestArmTGSignal(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST) // Tuesday
	svc, subs := tgService(&offlineFake{known: map[int64]bool{24500: true}})

	if res, err := svc.armTGSignal(context.Background(), tgCall(now), now); err != nil || res != strategy.TGArmed {
		t.Fatalf("arm: %v", err)
	}
	arms := svc.tgStrategy.Arms()
	if len(arms) != 1 || arms[0].FNOToken != "CE24500" || arms[0].FNOExchange != "NFO" || arms[0].IndexToken != "99926000" {
		t.Fatalf("arms %+v", arms)
	}
	if len(*subs) != 1 || (*subs)[0] != "CE24500" {
		t.Fatalf("subscribed %v", *subs)
	}

	// The armed call buys on the contract's premium crossing the trigger.
	sig := svc.tgStrategy.OnTick(model.Tick{Exchange: "NFO", Token: "CE24500", Price: 12050, EventTS: now.Add(time.Minute)})
	if sig == nil || sig.Action != strategy.ActionBuy || sig.FNOToken != "CE24500" {
		t.Fatalf("entry %+v", sig)
	}
}

func TestArmTGSignalRefusals(t *testing.T) {
	open := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST)
	cases := []struct {
		name string
		now  time.Time
		call model.TGSignal
		want string
	}{
		{"after close", open.Add(6 * time.Hour), tgCall(open.Add(6 * time.Hour)), "market closed"},
		{"past EOD cutoff", open.Add(5*time.Hour + 20*time.Minute), tgCall(open.Add(5*time.Hour + 20*time.Minute)), "market closed"},
		{"stale", open, tgCall(open.Add(-5 * time.Minute)), "stale"},
		{"unknown strike", open, func() model.TGSignal { c := tgCall(open); c.Strike = 99900; return c }(), "not in instrument master"},
		{"sensex not configured", open, func() model.TGSignal { c := tgCall(open); c.Underlying = "SENSEX"; return c }(), "not configured"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _ := tgService(&offlineFake{known: map[int64]bool{24500: true}})
			_, err := svc.armTGSignal(context.Background(), c.call, c.now)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}
}

// A call that does not match the market is refused before it is armed.
func TestArmTGSignalMarketChecks(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST)
	svc, _ := tgService(&offlineFake{known: map[int64]bool{24500: true}})
	svc.orderExecutor.UpdateLTP(model.Tick{Token: "99926000", Exchange: "NSE", Price: 2200000}) // spot 22000

	_, err := svc.armTGSignal(context.Background(), tgCall(now), now)
	if err == nil || !strings.Contains(err.Error(), "from NIFTY spot") {
		t.Fatalf("strike 24500 vs spot 22000: %v", err)
	}

	svc.orderExecutor.UpdateLTP(model.Tick{Token: "99926000", Exchange: "NSE", Price: 2440000}) // spot 24400
	svc.orderExecutor.UpdateLTP(model.Tick{Token: "CE24500", Exchange: "NFO", Price: 30000})    // premium 300 vs entry 120
	_, err = svc.armTGSignal(context.Background(), tgCall(now), now)
	if err == nil || !strings.Contains(err.Error(), "live premium") {
		t.Fatalf("entry 120 vs premium 300: %v", err)
	}

	svc.orderExecutor.UpdateLTP(model.Tick{Token: "CE24500", Exchange: "NFO", Price: 11500})
	if _, err := svc.armTGSignal(context.Background(), tgCall(now), now); err != nil {
		t.Fatalf("matching call refused: %v", err)
	}
}

func TestTGFollowUpStale(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST)
	svc, _ := tgService(&offlineFake{known: map[int64]bool{24500: true}})
	f := model.TGFollowUp{ChatID: 1, MsgID: 3, ReplyToMsgID: 2, Action: model.TGExit, ReceivedAt: now.Add(-5 * time.Minute)}
	if _, _, err := svc.tgFollowUp(f, now); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale follow-up: %v", err)
	}
}

// tgHolding is a service whose TELEGRAM_SIGNAL holds CE24500 at 120.
func tgHolding(t *testing.T) *Service {
	t.Helper()
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST)
	svc, _ := tgService(&offlineFake{known: map[int64]bool{24500: true}})
	svc.liveOrdersView = liveOrdersView{liveOrders: make(map[string]*liveOrderRuntime)}
	if _, err := svc.armTGSignal(context.Background(), tgCall(now), now); err != nil {
		t.Fatal(err)
	}
	if sig := svc.tgStrategy.OnTick(model.Tick{Exchange: "NFO", Token: "CE24500", Price: 12000, EventTS: now}); sig == nil {
		t.Fatal("no entry")
	}
	svc.upsertLiveOrder(strategy.TelegramSignalName, strategy.SideCall, "CE24500", 12000)
	return svc
}

func TestTGPositionCommands(t *testing.T) {
	svc := tgHolding(t)
	now := time.Now()
	set := model.PositionCommand{Strategy: strategy.TelegramSignalName, Side: "CALL", FNOToken: "NFO:CE24500",
		Action: model.PositionSetLevels, FNOSL: 11000, FNOTarget: 15000, TS: now}
	if sig, err := svc.positionCommand(cmdPayload(set), now); sig != nil || err != nil {
		t.Fatalf("set sig=%v err=%v", sig, err)
	}
	if p := svc.tgStrategy.Position(); p.SL != 11000 || p.Target != 15000 || !p.ManualSL {
		t.Fatalf("position %+v", p)
	}
	idx := set
	idx.FNOSL, idx.FNOTarget, idx.IndexStop = 0, 0, 2440000
	if _, err := svc.positionCommand(cmdPayload(idx), now); err == nil {
		t.Fatal("index stop accepted")
	}

	exit := model.PositionCommand{Strategy: strategy.TelegramSignalName, Side: "CALL", FNOToken: "NFO:CE24500", Action: model.PositionExit, TS: now}
	sig, err := svc.positionCommand(cmdPayload(exit), now)
	if err != nil || sig == nil || sig.Action != strategy.ActionExit || sig.StrategyName != strategy.TelegramSignalName {
		t.Fatalf("exit sig=%+v err=%v", sig, err)
	}

	svc.orderExecutor = orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]",
		LiveStrategies: []string{strategy.TelegramSignalName}})
	if _, err := svc.positionCommand(cmdPayload(exit), now); err == nil || !strings.Contains(err.Error(), "real orders") {
		t.Fatalf("real strategy edited: %v", err)
	}
}

func TestTGLiveOrderLevels(t *testing.T) {
	svc := tgHolding(t)
	p, ok := svc.liveOrderPayload(strategy.TelegramSignalName, strategy.SideCall)
	if !ok || p.StoplossPrice != 10000 || p.StoplossKind != "TG" || p.TargetPrice != 16800 {
		t.Fatalf("payload %+v", p)
	}
	if err := svc.tgStrategy.SetLevels(strategy.SideCall, "CE24500", strategy.LevelEdit{FNOSL: 11000}); err != nil {
		t.Fatal(err)
	}
	if p, _ := svc.liveOrderPayload(strategy.TelegramSignalName, strategy.SideCall); p.StoplossPrice != 11000 || p.StoplossKind != "MANUAL" {
		t.Fatalf("after hand stop %+v", p)
	}
}

func TestTGOutcomeLatency(t *testing.T) {
	svc := &Service{}
	recv := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST)
	if o := svc.tgOutcome("1:2", "ARMED", "", recv.Add(time.Second)); o.MS != 0 {
		t.Fatalf("unknown call timed: %+v", o)
	}
	svc.tgRecv.put("1:2", recv, recv)
	svc.tgRecv.put("1:3", time.Time{}, recv) // no receipt stamp: not kept
	if o := svc.tgOutcome("1:2", "ENTERED", "x", recv.Add(1500*time.Millisecond)); o.MS != 1500 || o.Event != "ENTERED" {
		t.Fatalf("outcome %+v", o)
	}
	if _, ok := svc.tgRecv.get("1:3"); ok {
		t.Fatal("zero receipt kept")
	}
	svc.tgRecv.put("1:4", recv.Add(25*time.Hour), recv.Add(25*time.Hour))
	if _, ok := svc.tgRecv.get("1:2"); ok {
		t.Fatal("day-old receipt kept")
	}
}
