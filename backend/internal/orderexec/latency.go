package orderexec

import (
	"log"
	"time"
)

// ObserveStage records how long one stage of the signal → broker order
// path took (Prometheus orderpath_stage_ms). Safe without metrics.
func (oe *OrderExecutor) ObserveStage(stage string, d time.Duration) {
	if oe.prom == nil || oe.prom.OrderStageMs == nil {
		return
	}
	oe.prom.OrderStageMs.WithLabelValues(stage).Observe(float64(d.Microseconds()) / 1000)
}

// stageTimes is one real order's pre-send and send timings.
type stageTimes struct {
	toExecutor, journal, persist, placeAck time.Duration
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// logStages logs and records one real order's timings.
func (oe *OrderExecutor) logStages(direction, strategy, symbol string, st stageTimes) {
	if st.toExecutor > 0 {
		oe.ObserveStage("signal_to_executor", st.toExecutor)
	}
	if direction == "BUY" {
		oe.ObserveStage("journal", st.journal)
		oe.ObserveStage("persist", st.persist)
	}
	oe.ObserveStage("place_ack", st.placeAck)
	log.Printf("%s ⏱ %s %s %s: signal→executor %.1fms | journal %.1fms | persist %.1fms | send→ack %.1fms",
		oe.logPrefix(), direction, strategy, symbol, ms(st.toExecutor), ms(st.journal), ms(st.persist), ms(st.placeAck))
}
