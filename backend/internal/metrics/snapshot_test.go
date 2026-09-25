package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestValue(t *testing.T) {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "snap_test_counter"})
	c.Add(3)
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "snap_test_gauge"})
	g.Set(1.5)

	if v := Value(c); v != 3 {
		t.Fatalf("counter Value = %v, want 3", v)
	}
	if v := Value(g); v != 1.5 {
		t.Fatalf("gauge Value = %v, want 1.5", v)
	}
	if v := Value(nil); v != 0 {
		t.Fatalf("nil Value = %v, want 0", v)
	}
}

func TestOrderSnapshot(t *testing.T) {
	m, _ := NewOrderMetrics()
	m.OrderCBState.Set(1)
	m.OrderCBFailures.Set(4)
	m.OrderRLCount.Set(2)
	m.OrderRLMax.Set(10)

	s := m.OrderSnapshot()
	if s.CBState != 1 || s.CBFailures != 4 || s.RLCount != 2 || s.RLMax != 10 {
		t.Fatalf("OrderSnapshot = %+v", s)
	}
}
