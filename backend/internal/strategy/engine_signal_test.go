package strategy

import (
	"testing"
	"time"
)

func TestTFEngine_SendSignal_ExitWaitsForRoomInsteadOfDropping(t *testing.T) {
	e := NewTFEngine(1)
	e.SendSignal(Signal{StrategyName: "s", Action: ActionBuy, Side: SideCall}) // fills buffer

	done := make(chan struct{})
	go func() {
		e.SendSignal(Signal{StrategyName: "s", Action: ActionExit, Side: SideCall})
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	<-e.Signals() // consumer frees a slot

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("exit send never completed")
	}
	got := <-e.Signals()
	if got.Action != ActionExit {
		t.Fatalf("exit signal dropped, got %+v", got)
	}
}

func TestTFEngine_SendSignal_EntryDroppedWhenFullAndCounted(t *testing.T) {
	e := NewTFEngine(1)
	e.SendSignal(Signal{StrategyName: "s", Action: ActionBuy, Side: SideCall})
	e.SendSignal(Signal{StrategyName: "s", Action: ActionBuy, Side: SidePut})
	if n := e.DroppedSignals(); n != 1 {
		t.Fatalf("want 1 dropped entry, got %d", n)
	}
}

func TestTFEngine_SendSignal_ExitGivesUpAfterTimeout(t *testing.T) {
	old := exitSendTimeout
	exitSendTimeout = 10 * time.Millisecond
	defer func() { exitSendTimeout = old }()

	e := NewTFEngine(1)
	e.SendSignal(Signal{StrategyName: "s", Action: ActionBuy, Side: SideCall})
	e.SendSignal(Signal{StrategyName: "s", Action: ActionExit, Side: SideCall}) // nobody consuming
	if n := e.DroppedSignals(); n != 1 {
		t.Fatalf("timed-out exit must be counted, got %d", n)
	}
}

func TestTFEngine_DroppedExitCallsHook(t *testing.T) {
	old := exitSendTimeout
	exitSendTimeout = 5 * time.Millisecond
	defer func() { exitSendTimeout = old }()

	e := NewTFEngine(1)
	var dropped []Signal
	e.OnExitDropped = func(s Signal) { dropped = append(dropped, s) }
	e.SendSignal(Signal{StrategyName: "s", Action: ActionBuy, Side: SideCall})
	e.SendSignal(Signal{StrategyName: "s", Action: ActionExit, Side: SideCall})
	if len(dropped) != 1 || dropped[0].Action != ActionExit {
		t.Fatalf("want exit-drop hook called once, got %+v", dropped)
	}
}
