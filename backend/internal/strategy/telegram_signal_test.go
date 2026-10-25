package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

var tgT0 = time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)

func tgArm() TGArm {
	return TGArm{
		Call: model.TGSignal{ChatID: 1, MsgID: 9, Underlying: "NIFTY", Strike: 24500, OptionType: "CE",
			TriggerPaise: 12000, SLPaise: 10000, Targets: []int64{14000, 16000}},
		IndexToken: "99926000", IndexExchange: "NSE",
		FNOExchange: "NFO", FNOToken: "45001", FNOSymbol: "NIFTY28OCT2624500CE", ArmedAt: tgT0,
	}
}

func tgTick(token string, price int64, at time.Duration) model.Tick {
	return model.Tick{Exchange: "NFO", Token: token, Price: price, EventTS: tgT0.Add(at)}
}

func newTG(t *testing.T) *TelegramSignal {
	s := NewTelegramSignal(65, TelegramSignalConfig{TargetPct: 40, MaxChasePct: 5, MaxPriceGapPct: 40, ArmFor: 30 * time.Minute})
	if _, err := s.Arm(tgArm()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTGEntryOnTriggerThenTarget(t *testing.T) {
	s := newTG(t)
	if sig := s.OnTick(tgTick("45001", 11900, time.Minute)); sig != nil {
		t.Fatalf("entered below trigger: %+v", sig)
	}
	if sig := s.OnTick(tgTick("99999", 13000, time.Minute)); sig != nil {
		t.Fatal("other contract triggered")
	}
	sig := s.OnTick(tgTick("45001", 12100, 2*time.Minute))
	if sig == nil || sig.Action != ActionBuy || sig.Side != SideCall || sig.FNOToken != "45001" ||
		sig.Strike != 24500 || sig.Token != "99926000" || sig.Price != 12100 {
		t.Fatalf("entry %+v", sig)
	}
	if p := s.Position(); p == nil || p.Target != 16940 || p.SL != 10000 {
		t.Fatalf("position %+v", p)
	}
	if _, err := s.Arm(tgArm()); err == nil {
		t.Fatal("armed while holding")
	}
	// The caller's 160 target is not ours.
	if sig := s.OnTick(tgTick("45001", 16000, 3*time.Minute)); sig != nil {
		t.Fatalf("exited at caller target: %+v", sig)
	}
	sig = s.OnTick(tgTick("45001", 17000, 4*time.Minute))
	if sig == nil || sig.Action != ActionExit || !strings.HasPrefix(sig.Reason, "TG TARGET") || sig.EntryFNOPrice != 12100 {
		t.Fatalf("target exit %+v", sig)
	}
	if s.Position() != nil {
		t.Fatal("position kept after exit")
	}
}

func TestTGStopLoss(t *testing.T) {
	s := newTG(t)
	s.OnTick(tgTick("45001", 12000, time.Minute))
	sig := s.OnTick(tgTick("45001", 10000, 2*time.Minute))
	if sig == nil || sig.Action != ActionExit || !strings.HasPrefix(sig.Reason, "TG SL") {
		t.Fatalf("sl exit %+v", sig)
	}
}

func TestTGChaseCapAndStopBeforeTrigger(t *testing.T) {
	s := newTG(t)
	if sig := s.OnTick(tgTick("45001", 12700, time.Minute)); sig != nil { // > 126
		t.Fatalf("chased: %+v", sig)
	}
	if len(s.Arms()) != 0 {
		t.Fatal("missed call still armed")
	}

	s = newTG(t)
	s.OnTick(tgTick("45001", 9900, time.Minute))
	if len(s.Arms()) != 0 {
		t.Fatal("call kept after stop hit before trigger")
	}
}

func TestTGExpiryAndCancel(t *testing.T) {
	s := newTG(t)
	if sig := s.OnTick(tgTick("45001", 12100, 31*time.Minute)); sig != nil {
		t.Fatalf("expired call entered: %+v", sig)
	}

	s = newTG(t)
	s.OnTick(tgTick("45001", 12100, time.Minute))
	s.CancelEntry(SideCall, "market closed")
	if s.Position() != nil {
		t.Fatal("cancelled entry still held")
	}
}

func TestTGForceExitAndRestore(t *testing.T) {
	s := newTG(t)
	s.OnTick(tgTick("45001", 12100, time.Minute))
	p := *s.Position()
	sigs := s.ForceExitAll("EOD")
	if len(sigs) != 1 || sigs[0].Action != ActionExit || s.Position() != nil {
		t.Fatalf("force exit %+v", sigs)
	}
	s.Restore(p)
	if sig := s.ExitRequested(SideCall, "NFO:45001", "MANUAL EXIT"); sig == nil {
		t.Fatal("restored position not exitable")
	}
}

func tgFollow(reply int, action model.TGFollowUpAction, sl int64) model.TGFollowUp {
	return model.TGFollowUp{ChatID: 1, MsgID: 50, ReplyToMsgID: reply, Action: action, NewSLPaise: sl}
}

func TestTGFollowUpExitOnlyForLinkedCall(t *testing.T) {
	s := newTG(t)
	s.OnTick(tgTick("45001", 12100, time.Minute))

	// A reply to another message never touches our trade.
	if sig, _, err := s.FollowUp(tgFollow(8, model.TGExit, 0)); sig != nil || err == nil || !strings.Contains(err.Error(), "UNLINKED") {
		t.Fatalf("other reply: %+v %v", sig, err)
	}
	// Not a reply, no strike named: ambiguous, ignored.
	if sig, _, err := s.FollowUp(tgFollow(0, model.TGExit, 0)); sig != nil || err == nil {
		t.Fatalf("non-reply: %+v %v", sig, err)
	}
	sig, res, err := s.FollowUp(tgFollow(9, model.TGSLHit, 0))
	if err != nil || res != "EXIT" || sig == nil || sig.Action != ActionExit || !strings.Contains(sig.Reason, "TG CALLER SL_HIT") {
		t.Fatalf("linked exit: %+v %s %v", sig, res, err)
	}
	if s.Position() != nil {
		t.Fatal("position kept")
	}
}

func TestTGFollowUpNonReplyNamingStrike(t *testing.T) {
	s := newTG(t)
	s.OnTick(tgTick("45001", 12100, time.Minute))
	f := tgFollow(0, model.TGExit, 0)
	f.Strike, f.OptionType = 24500, "PE"
	if _, _, err := s.FollowUp(f); err == nil {
		t.Fatal("wrong option type accepted")
	}
	f.OptionType = "CE"
	if sig, _, err := s.FollowUp(f); err != nil || sig == nil {
		t.Fatalf("named strike: %+v %v", sig, err)
	}
}

func TestTGFollowUpMoveSLOnlyUp(t *testing.T) {
	s := newTG(t)
	s.OnTick(tgTick("45001", 12100, time.Minute))
	s.OnTick(tgTick("45001", 13000, 2*time.Minute))
	if _, _, err := s.FollowUp(tgFollow(9, model.TGMoveSL, 9000)); err == nil {
		t.Fatal("stop lowered")
	}
	if _, _, err := s.FollowUp(tgFollow(9, model.TGMoveSL, 13500)); err == nil {
		t.Fatal("stop above premium accepted")
	}
	if _, res, err := s.FollowUp(tgFollow(9, model.TGMoveSL, 12000)); err != nil || res != TGSLRaised {
		t.Fatalf("SL cost: %s %v", res, err)
	}
	if sig := s.OnTick(tgTick("45001", 11950, 3*time.Minute)); sig == nil || !strings.HasPrefix(sig.Reason, "TG RAISED STOP") {
		t.Fatalf("raised stop did not exit: %+v", sig)
	}
}

func TestTGFollowUpBeforeEntry(t *testing.T) {
	s := newTG(t)
	if _, res, err := s.FollowUp(tgFollow(9, model.TGMoveSL, 11000)); err != nil || res != TGSLRaised || s.Arms()[0].Call.SLPaise != 11000 {
		t.Fatalf("waiting move: %s %v", res, err)
	}
	if sig, res, err := s.FollowUp(tgFollow(9, model.TGCancel, 0)); sig != nil || err != nil || res != "DROPPED" || len(s.Arms()) != 0 {
		t.Fatalf("cancel: %+v %s %v", sig, res, err)
	}
}

func TestTGEditsAndPriceGap(t *testing.T) {
	s := newTG(t)
	// Edit before entry replaces the waiting call (same message).
	ed := tgArm()
	ed.Call.Edited, ed.Call.TriggerPaise, ed.Call.SLPaise = true, 12500, 11000
	ed.Call.Targets = nil
	if res, err := s.Arm(ed); err != nil || res != TGReplaced || s.Arms()[0].Call.TriggerPaise != 12500 {
		t.Fatalf("edit before entry: %s %v %+v", res, err, s.Arms())
	}
	s.OnTick(tgTick("45001", 12600, time.Minute))
	// Edit after entry: a lower stop is refused, a higher one applies.
	ed.Call.SLPaise = 10500
	if res, _ := s.Arm(ed); res != TGEditIgnored {
		t.Fatalf("lower stop edit: %s", res)
	}
	ed.Call.SLPaise = 11500
	if res, err := s.Arm(ed); err != nil || res != TGSLRaised || s.Position().SL != 11500 {
		t.Fatalf("higher stop edit: %s %v", res, err)
	}

	// First premium far from the entry: misread call, dropped.
	g := newTG(t)
	g.OnTick(tgTick("45001", 30000, time.Minute))
	if len(g.Arms()) != 0 {
		t.Fatal("call kept with premium 2.5x its entry")
	}
}

func TestTGEvents(t *testing.T) {
	s := newTG(t)
	var got []string
	s.SetOnEvent(func(k, e, _ string) { got = append(got, k+" "+e) })
	s.OnTick(tgTick("45001", 12100, time.Minute))
	s.OnTick(tgTick("45001", 9000, 2*time.Minute))
	if strings.Join(got, ",") != "1:9 FIRST_TICK,1:9 ENTERED,1:9 EXIT" {
		t.Fatalf("events %v", got)
	}
}

// The 22800 PE call: entry 172, SL 166, T1 184, T2 194. No caller exit.
func ladderTG(t *testing.T, targets []int64) *TelegramSignal {
	s := NewTelegramSignal(65, TelegramSignalConfig{MaxChasePct: 5, MaxPriceGapPct: 40, LadderTrailPct: 10, ArmFor: 30 * time.Minute})
	a := tgArm()
	a.Call.TriggerPaise, a.Call.SLPaise, a.Call.Targets = 17200, 16600, targets
	if _, err := s.Arm(a); err != nil {
		t.Fatal(err)
	}
	if sig := s.OnTick(tgTick("45001", 17200, time.Minute)); sig == nil || sig.Action != ActionBuy {
		t.Fatalf("no entry: %+v", sig)
	}
	return s
}

func TestTGLadder(t *testing.T) {
	s := ladderTG(t, []int64{18400, 19400})
	steps := []struct {
		price, stop int64
	}{
		{18000, 16600}, // below T1: call's SL
		{18400, 17200}, // T1: cost
		{17500, 17200}, // dip: stop never moves down
		{19400, 18400}, // T2: stop to T1
		{22700, 20430}, // past last target: 10% below best 227
		{21500, 20430}, // dip above the stop: hold
	}
	for i, st := range steps {
		if sig := s.OnTick(tgTick("45001", st.price, time.Duration(2+i)*time.Minute)); sig != nil {
			t.Fatalf("step %d price %d: unexpected exit %+v", i, st.price, sig)
		}
		if got := s.Position().SL; got != st.stop {
			t.Fatalf("step %d price %d: stop %d, want %d", i, st.price, got, st.stop)
		}
	}
	sig := s.OnTick(tgTick("45001", 20400, 10*time.Minute))
	if sig == nil || sig.Action != ActionExit || !strings.HasPrefix(sig.Reason, "TG RAISED STOP") {
		t.Fatalf("ladder exit: %+v", sig)
	}
}

func TestTGLadderNoTargets(t *testing.T) {
	s := ladderTG(t, nil)
	s.OnTick(tgTick("45001", 18800, 2*time.Minute)) // < +10% (189.2)
	if s.Position().SL != 16600 {
		t.Fatalf("stop moved before +10%%: %d", s.Position().SL)
	}
	s.OnTick(tgTick("45001", 18920, 3*time.Minute)) // +10%: cost (trail 170.28 is lower)
	if s.Position().SL != 17200 {
		t.Fatalf("stop at +10%%: %d, want 17200", s.Position().SL)
	}
	s.OnTick(tgTick("45001", 22000, 4*time.Minute))
	if s.Position().SL != 19800 {
		t.Fatalf("trail: %d, want 19800", s.Position().SL)
	}
}

func TestTGLadderAndFollowUpHigherWins(t *testing.T) {
	s := ladderTG(t, []int64{18400, 19400})
	s.OnTick(tgTick("45001", 18000, 2*time.Minute))
	// "Sl ctc kar lena" before T1: stop to the call's entry.
	if _, _, err := s.FollowUp(tgFollow(9, model.TGMoveSL, 17200)); err != nil {
		t.Fatal(err)
	}
	s.OnTick(tgTick("45001", 19400, 3*time.Minute))
	if s.Position().SL != 18400 {
		t.Fatalf("ladder after follow-up: %d", s.Position().SL)
	}
	// A later follow-up below the ladder stop is refused.
	if _, _, err := s.FollowUp(tgFollow(9, model.TGMoveSL, 17500)); err == nil {
		t.Fatal("follow-up lowered the ladder stop")
	}
}

func TestTGLadderSurvivesRestore(t *testing.T) {
	s := ladderTG(t, []int64{18400, 19400})
	s.OnTick(tgTick("45001", 19400, 2*time.Minute))
	p := *s.Position()

	r := NewTelegramSignal(65, TelegramSignalConfig{LadderTrailPct: 10})
	r.Restore(p)
	r.OnTick(tgTick("45001", 22700, 3*time.Minute))
	if r.Position().SL != 20430 {
		t.Fatalf("restored ladder: %d", r.Position().SL)
	}
}

func TestTGSetLevels(t *testing.T) {
	s := newTG(t)
	var events []string
	s.SetOnEvent(func(_, ev, _ string) { events = append(events, ev) })
	if err := s.SetLevels(SideCall, "45001", LevelEdit{FNOSL: 11000}); err == nil {
		t.Fatal("edit with no position")
	}
	s.OnTick(tgTick("45001", 12000, time.Minute))

	refused := map[string]struct {
		side PositionSide
		tok  string
		e    LevelEdit
	}{
		"index stop":         {SideCall, "45001", LevelEdit{IndexStop: 2400000}},
		"wrong side":         {SidePut, "45001", LevelEdit{FNOSL: 11000}},
		"wrong token":        {SideCall, "45002", LevelEdit{FNOSL: 11000}},
		"stop above LTP":     {SideCall, "45001", LevelEdit{FNOSL: 12000}},
		"target below LTP":   {SideCall, "45001", LevelEdit{FNOTarget: 11900}},
		"stop ok, tgt wrong": {SideCall, "45001", LevelEdit{FNOSL: 11000, FNOTarget: 12000}},
	}
	for name, c := range refused {
		if err := s.SetLevels(c.side, c.tok, c.e); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if p := s.Position(); p.SL != 10000 || p.Target != 16800 || p.ManualSL {
		t.Fatalf("refused edit changed position %+v", p)
	}

	// A hand stop may sit below the call's stop too; the user decides.
	if err := s.SetLevels(SideCall, "NFO:45001", LevelEdit{FNOSL: 9000, FNOTarget: 15000}); err != nil {
		t.Fatal(err)
	}
	if p := s.Position(); p.SL != 9000 || p.Target != 15000 || !p.ManualSL {
		t.Fatalf("after edit %+v", p)
	}
	if err := s.SetLevels(SideCall, "", LevelEdit{FNOTarget: -1}); err != nil {
		t.Fatal(err)
	}
	if p := s.Position(); p.Target != 0 || p.SL != 9000 {
		t.Fatalf("target not removed %+v", p)
	}
	if strings.Join(events, ",") != "FIRST_TICK,ENTERED,LEVELS_SET,LEVELS_SET" {
		t.Fatalf("events %v", events)
	}
	if sig := s.OnTick(tgTick("45001", 9500, 2*time.Minute)); sig != nil {
		t.Fatalf("old stop still active: %+v", sig)
	}
	sig := s.OnTick(tgTick("45001", 9000, 3*time.Minute))
	if sig == nil || sig.Action != ActionExit || !strings.HasPrefix(sig.Reason, "TG MANUAL STOP") {
		t.Fatalf("manual stop exit %+v", sig)
	}
}
