package omsvc

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/oms"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

type fakeExec struct{ ran []strategy.Signal }

func (f *fakeExec) ExecuteSignal(s strategy.Signal)                  { f.ran = append(f.ran, s) }
func (f *fakeExec) UpdateLTP(model.Tick)                             {}
func (f *fakeExec) Snapshot() ([]byte, error)                        { return nil, errors.New("no snapshot in test") }
func (f *fakeExec) Restore([]byte) error                             { return nil }
func (f *fakeExec) GetEntryOrders() map[string]orderexec.OrderRecord { return nil }

type fakeClaims struct {
	seen map[string]bool
	err  error
}

func (c *fakeClaims) ClaimIntent(id string, _ time.Time) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if c.seen[id] {
		return false, nil
	}
	c.seen[id] = true
	return true, nil
}

// testService has no Redis: handle's publish/persist calls must tolerate
// that, so the service gets a bus on an unreachable client.
func testService(ex *fakeExec, cl *fakeClaims, now time.Time) *Service {
	s := &Service{cfg: Config{EntryTTL: 30 * time.Second}, oe: ex, claims: cl, now: func() time.Time { return now }}
	s.rdb = unreachableRedis()
	s.bus = oms.NewBus(s.rdb)
	return s
}

func intent(id string, action strategy.Action, created time.Time) oms.Intent {
	p, _ := json.Marshal(strategy.Signal{StrategyName: "NIFTY50_SR", Action: action, Side: strategy.SideCall})
	return oms.Intent{ID: id, Action: string(action), Payload: p, CreatedTS: created}
}

func TestHandle_RunsOnceAndSkipsDuplicate(t *testing.T) {
	now := time.Now()
	ex := &fakeExec{}
	s := testService(ex, &fakeClaims{}, now)
	if err := s.handle(intent("a", strategy.ActionBuy, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(intent("a", strategy.ActionBuy, now)); err == nil {
		t.Fatal("duplicate intent not reported")
	}
	if len(ex.ran) != 1 {
		t.Fatalf("executed %d times, want 1", len(ex.ran))
	}
}

func TestHandle_StaleEntrySkippedExitRun(t *testing.T) {
	now := time.Now()
	ex := &fakeExec{}
	s := testService(ex, &fakeClaims{}, now)
	old := now.Add(-time.Minute)
	if err := s.handle(intent("e", strategy.ActionBuy, old)); err == nil {
		t.Fatal("stale entry not reported")
	}
	if err := s.handle(intent("x", strategy.ActionExit, old)); err != nil {
		t.Fatal(err)
	}
	if len(ex.ran) != 1 || ex.ran[0].Action != strategy.ActionExit {
		t.Fatalf("ran %+v, want only the exit", ex.ran)
	}
}

func TestHandle_ClaimFailureSkipsEntryRunsExit(t *testing.T) {
	now := time.Now()
	ex := &fakeExec{}
	s := testService(ex, &fakeClaims{err: errors.New("disk full")}, now)
	_ = s.handle(intent("e", strategy.ActionBuy, now))
	_ = s.handle(intent("x", strategy.ActionExit, now))
	if len(ex.ran) != 1 || ex.ran[0].Action != strategy.ActionExit {
		t.Fatalf("ran %+v, want only the exit", ex.ran)
	}
}
