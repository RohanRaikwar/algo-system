package orderexec

import (
	"testing"
	"time"

	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
)

// A real BUY records its pre-send and send stages.
func TestStagesRecorded(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	m, reg := metrics.NewOrderMetrics()
	oe.AttachMetrics(m)
	s := realSig(model.ActionBuy)
	s.EmittedAt = time.Now().Add(-3 * time.Millisecond)
	oe.ExecuteSignal(s)

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string]bool{}
	for _, mf := range families {
		if mf.GetName() != "orderpath_stage_ms" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == "stage" && metric.GetHistogram().GetSampleCount() > 0 {
					stages[l.GetValue()] = true
				}
			}
		}
	}
	for _, want := range []string{"signal_to_executor", "journal", "persist", "place_ack"} {
		if !stages[want] {
			t.Errorf("stage %s not recorded (got %v)", want, stages)
		}
	}
}
