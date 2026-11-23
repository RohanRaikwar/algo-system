package strategy

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// With EntryCapPaise the Active entry carries the exchange order: trigger
// = the call's entry, limit = entry + cap, expiry = the arm window. A
// premium past the old chase limit still buys (LIMIT at the cap), and the
// fill corrects the entry.
func TestTGEntryCap(t *testing.T) {
	s := NewTelegramSignal("TG_VIKASH", 65, TelegramSignalConfig{MaxChasePct: 5, MaxPriceGapPct: 40, ArmFor: 15 * time.Minute,
		WaitActive: true, EntryCapPaise: 100, TargetPct: 40})
	if _, err := s.Arm(tgArm()); err != nil {
		t.Fatal(err)
	}
	s.OnTick(tgTick("45001", 13000, time.Minute)) // 8% above 120: old chase limit would drop it
	sig, res, err := s.FollowUp(tgFollow(9, model.TGActivate, 0))
	if err != nil || res != "ENTERED" || sig == nil {
		t.Fatalf("active: %+v %s %v", sig, res, err)
	}
	if sig.EntryTriggerPaise != 0 || sig.EntryLimitPaise != 12100 || !sig.EntryExpiresAt.Equal(tgT0.Add(15*time.Minute)) {
		t.Fatalf("entry order %d/%d/%v", sig.EntryTriggerPaise, sig.EntryLimitPaise, sig.EntryExpiresAt)
	}
	if p := s.Position(); p == nil || p.Entry != 12000 || p.Target != 16800 {
		t.Fatalf("provisional position %+v", p)
	}
	s.FillEntry(SideCall, 12050)
	if p := s.Position(); p.Entry != 12050 || p.Target != 16870 {
		t.Fatalf("filled position %+v", p)
	}
	s.CancelEntry(SideCall, "resting BUY unfilled")
	if s.Position() != nil {
		t.Fatal("position kept after the entry dropped")
	}
}

// On Active the buy goes out in the same step, never waiting for our
// premium feed: no tick seen yet, or a premium under the stop.
func TestTGEntryCapActiveNoWait(t *testing.T) {
	newCap := func(t *testing.T) *TelegramSignal {
		s := NewTelegramSignal("TG_TCI", 65, TelegramSignalConfig{MaxPriceGapPct: 40, ArmFor: 15 * time.Minute, WaitActive: true, EntryCapPaise: 100})
		if _, err := s.Arm(tgArm()); err != nil {
			t.Fatal(err)
		}
		return s
	}
	active := tgFollow(9, model.TGActivate, 0)

	s := newCap(t)
	sig, res, err := s.FollowUp(active)
	if err != nil || res != "ENTERED" || sig == nil || sig.Price != 12000 || sig.EntryTriggerPaise != 0 || sig.EntryLimitPaise != 12100 {
		t.Fatalf("no premium seen: %+v %s %v", sig, res, err)
	}

	s = newCap(t)
	s.OnTick(tgTick("45001", 9500, time.Minute)) // under the stop 100, never above it
	sig, res, err = s.FollowUp(active)
	if err != nil || res != "ENTERED" || sig == nil || sig.Price != 9500 {
		t.Fatalf("premium under stop: %+v %s %v", sig, res, err)
	}

	// A price strategy keeps the exchange trigger.
	p := NewTelegramSignal("", 65, TelegramSignalConfig{MaxPriceGapPct: 40, ArmFor: 15 * time.Minute, EntryCapPaise: 100})
	if _, err := p.Arm(tgArm()); err != nil {
		t.Fatal(err)
	}
	if sig := p.OnTick(tgTick("45001", 12050, time.Minute)); sig == nil || sig.EntryTriggerPaise != 12000 || sig.EntryLimitPaise != 12100 {
		t.Fatalf("price entry %+v", sig)
	}
}

// Caller edits the call to another strike after Active, before our entry
// filled: the entry is exited (resting order cancelled) and the edited
// call waits for a new Active. After the fill, the same edit is ignored.
func TestTGEditBeforeAndAfterFill(t *testing.T) {
	newActive := func(t *testing.T) *TelegramSignal {
		s := NewTelegramSignal("TG_TCI", 65, TelegramSignalConfig{MaxPriceGapPct: 40, ArmFor: 15 * time.Minute, WaitActive: true, EntryCapPaise: 100})
		if _, err := s.Arm(tgArm()); err != nil {
			t.Fatal(err)
		}
		if sig, _, err := s.FollowUp(tgFollow(9, model.TGActivate, 0)); err != nil || sig == nil {
			t.Fatalf("active: %v", err)
		}
		return s
	}
	edit := tgArm()
	edit.Call.Edited, edit.Call.Strike = true, 24600
	edit.FNOToken, edit.FNOSymbol = "45002", "NIFTY28OCT2624600CE"

	s := newActive(t)
	sig := s.ReplaceResting(edit)
	if sig == nil || sig.Action != ActionExit || sig.FNOToken != "45001" {
		t.Fatalf("replace signal %+v", sig)
	}
	if s.Position() != nil || len(s.Arms()) != 1 || s.Arms()[0].FNOToken != "45002" || s.Arms()[0].Active {
		t.Fatalf("after replace: pos %+v arms %+v", s.Position(), s.Arms())
	}

	// Only the stop changed: not a replace.
	s = newActive(t)
	slOnly := tgArm()
	slOnly.Call.Edited, slOnly.Call.SLPaise = true, 10500
	if s.ReplaceResting(slOnly) != nil {
		t.Fatal("stop-only edit replaced the entry")
	}

	// Filled: kept, edit ignored with a reason.
	s = newActive(t)
	s.FillEntry(SideCall, 12050)
	if s.ReplaceResting(edit) != nil {
		t.Fatal("filled entry replaced")
	}
	res, err := s.Arm(edit)
	if res != TGEditIgnored || err == nil || s.Position() == nil {
		t.Fatalf("held edit: %s %v", res, err)
	}
}
