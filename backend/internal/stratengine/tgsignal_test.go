package stratengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/internal/underlying"
)

func tgService(r *offlineFake) (*Service, *[]string) {
	var subs []string
	svc := &Service{
		legResolver:   r,
		orderExecutor: orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"}),
		cfg:           Config{EODExitTime: "15:15", TGArmMin: 30, TGMaxStrikeGapPct: 3, TGMaxPriceGapPct: 40},
	}
	svc.tgStrategies = []*strategy.TelegramSignal{strategy.NewTelegramSignal("", 65, strategy.TelegramSignalConfig{TargetPct: 40, MaxChasePct: 5})}
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
	arms := svc.tgByName(strategy.TelegramSignalName).Arms()
	if len(arms) != 1 || arms[0].FNOToken != "CE24500" || arms[0].FNOExchange != "NFO" || arms[0].IndexToken != "99926000" {
		t.Fatalf("arms %+v", arms)
	}
	if len(*subs) != 1 || (*subs)[0] != "CE24500" {
		t.Fatalf("subscribed %v", *subs)
	}

	// The armed call buys on the contract's premium crossing the trigger.
	sig := svc.tgByName(strategy.TelegramSignalName).OnTick(model.Tick{Exchange: "NFO", Token: "CE24500", Price: 12050, EventTS: now.Add(time.Minute)})
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
	if sig := svc.tgByName(strategy.TelegramSignalName).OnTick(model.Tick{Exchange: "NFO", Token: "CE24500", Price: 12000, EventTS: now}); sig == nil {
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
	if p := svc.tgByName(strategy.TelegramSignalName).Position(); p.SL != 11000 || p.Target != 15000 || !p.ManualSL {
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
	if err := svc.tgByName(strategy.TelegramSignalName).SetLevels(strategy.SideCall, "CE24500", strategy.LevelEdit{FNOSL: 11000}); err != nil {
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

// A dropped call shows on the index chart as a refused entry.
func TestTGDropRecordsRefusedAtIndex(t *testing.T) {
	svc, _ := tgService(&offlineFake{known: map[int64]bool{24500: true}})
	sent := make(chan string, 1)
	svc.refusedPublishHook = func(p string) { sent <- p }
	call := tgCall(time.Now())
	call.Text = "BUY NIFTY 24500 CE above 120 SL 100"
	svc.onTGDrop(strategy.TelegramSignalName, strategy.TGArm{Call: call, IndexToken: "99926000", IndexExchange: "NSE"}, "premium 9900 hit stop 10000 before entry 12000")

	var v refusedView
	select {
	case p := <-sent:
		if err := json.Unmarshal([]byte(p), &v); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no refused entry published")
	}
	e := v.Entries[len(v.Entries)-1]
	if e.Strategy != strategy.TelegramSignalName || e.Token != "99926000" || e.Exchange != "NSE" || e.Side != "CALL" ||
		e.Strike != 24500 || e.StrategyReason != call.Text || !strings.Contains(e.Reason, "dropped: premium 9900") {
		t.Fatalf("entry %+v", e)
	}
}

func TestTGIndexOf(t *testing.T) {
	svc, _ := tgService(&offlineFake{})
	if tok, ex := svc.tgIndexOf("NIFTY"); tok != "99926000" || ex != "NSE" {
		t.Fatalf("NIFTY = %s:%s", ex, tok)
	}
	if tok, ex := svc.tgIndexOf("SENSEX"); tok != "" || ex != "" {
		t.Fatalf("unconfigured SENSEX = %s:%s", ex, tok)
	}
}

// Each STRAT_TG_CHANNELS chat holds its own trade; other chats trade as
// TELEGRAM_SIGNAL. A follow-up only reaches its own channel's trade.
func TestTGOneTradePerChannel(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, markethours.IST)
	svc, _ := tgService(&offlineFake{known: map[int64]bool{24500: true}})
	cfg := svc.cfg
	cfg.Qty, cfg.TGMaxChasePct, cfg.TGChannels = 65, 5, map[int64]string{7: "TG_VIKASH", 8: "TG_TCI"}
	if got := len(svc.buildTGStrategies(cfg)); got != 3 {
		t.Fatalf("%d strategies, want 3", got)
	}
	if svc.tgForChat(7).Name() != "TG_VIKASH" || svc.tgForChat(8).Name() != "TG_TCI" || svc.tgForChat(1).Name() != strategy.TelegramSignalName {
		t.Fatal("chat routing")
	}

	tick := model.Tick{Exchange: "NFO", Token: "CE24500", Price: 12050, EventTS: now.Add(time.Minute)}
	for _, chat := range []int64{1, 7, 8} {
		call := tgCall(now)
		call.ChatID = chat
		if _, err := svc.armTGSignal(context.Background(), call, now); err != nil {
			t.Fatalf("chat %d arm: %v", chat, err)
		}
	}
	for _, tg := range svc.tgStrategies {
		if sig := tg.OnTick(tick); sig == nil || sig.Action != strategy.ActionBuy || sig.StrategyName != tg.Name() {
			t.Fatalf("%s entry %+v", tg.Name(), sig)
		}
	}

	// The caller of chat 7 exits: only TG_VIKASH closes.
	sig, _, err := svc.tgFollowUp(model.TGFollowUp{ChatID: 7, MsgID: 9, ReplyToMsgID: 2, Action: model.TGExit, ReceivedAt: now}, now)
	if err != nil || sig == nil || sig.StrategyName != "TG_VIKASH" || sig.Action != strategy.ActionExit {
		t.Fatalf("follow-up %+v %v", sig, err)
	}
	if svc.tgByName("TG_VIKASH").Position() != nil || svc.tgByName("TG_TCI").Position() == nil || svc.tgByName(strategy.TelegramSignalName).Position() == nil {
		t.Fatal("follow-up reached another channel's trade")
	}

	// EOD closes every channel still open.
	names := map[string]bool{}
	for _, s := range svc.forceExitAllStrategies("EOD") {
		names[s.StrategyName] = true
	}
	if len(names) != 2 || !names["TG_TCI"] || !names[strategy.TelegramSignalName] {
		t.Fatalf("EOD exits %v", names)
	}
}

func TestParseTGChannels(t *testing.T) {
	got := parseTGChannels("4340040418=TG_VIKASH, -1004370794640=tg_tci, 5=NIFTY50_SR, x=TG_X, 6=TG_")
	if len(got) != 2 || got[4340040418] != "TG_VIKASH" || got[4370794640] != "TG_TCI" {
		t.Fatalf("channels %v", got)
	}
	if model.TGPositionKeyFor(strategy.TelegramSignalName) != model.TGPositionKey || model.TGPositionKeyFor("TG_TCI") != model.TGPositionKey+":TG_TCI" {
		t.Fatal("position keys")
	}
}

// A SENSEX call carries SENSEX's paper size (lots × 20), not STRAT_QTY.
func TestTGQtyPerUnderlying(t *testing.T) {
	svc, _ := tgService(&offlineFake{})
	svc.cfg.Qty, svc.cfg.LiveLots = 65, 1
	if got := svc.tgQtyOf(underlying.Primary()); got != 65 {
		t.Fatalf("NIFTY qty %d, want 65", got)
	}
	if got := svc.tgQtyOf(underlying.Get("SENSEX")); got != 20 {
		t.Fatalf("SENSEX qty %d, want 20", got)
	}

	tg := strategy.NewTelegramSignal("", 65, strategy.TelegramSignalConfig{MaxChasePct: 5})
	a := strategy.TGArm{Call: tgCall(time.Now()), FNOExchange: "BFO", FNOToken: "S1", Qty: 20, ArmedAt: time.Now()}
	if _, err := tg.Arm(a); err != nil {
		t.Fatal(err)
	}
	buy := tg.OnTick(model.Tick{Exchange: "BFO", Token: "S1", Price: 12000, EventTS: time.Now()})
	if buy == nil || buy.Qty != 20 {
		t.Fatalf("entry %+v", buy)
	}
	exits := tg.ForceExitAll("EOD")
	if len(exits) != 1 || exits[0].Qty != 20 {
		t.Fatalf("exit %+v", exits)
	}
}
