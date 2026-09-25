package metrics

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// NewOrderMetrics exposes only what stratengine owns: orderexec_* series and
// no zero-valued mdengine_* series to pollute dashboards.
func TestNewOrderMetrics_RegistersOnlyOrderSeries(t *testing.T) {
	m, reg := NewOrderMetrics()
	m.OrderRLMax.Set(7)
	m.OrderCBState.Set(1)
	m.OrderCBFailures.Set(2)
	m.OrderRLCount.Set(3)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, mf := range mfs {
		name := mf.GetName()
		seen[name] = true
		if strings.HasPrefix(name, "mdengine_") || strings.HasPrefix(name, "indengine_") {
			t.Errorf("order registry exports %s", name)
		}
	}
	for _, want := range []string{"orderexec_rate_limiter_max", "orderexec_circuit_breaker_state", "orderexec_circuit_breaker_failures", "orderexec_rate_limiter_count"} {
		if !seen[want] {
			t.Errorf("order registry missing %s", want)
		}
	}
}

// NewServerWithRegistry serves /metrics from the given registry only.
func TestNewServerWithRegistry_ServesRegistry(t *testing.T) {
	m, reg := NewOrderMetrics()
	m.OrderRLMax.Set(9)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv := NewServerWithRegistry(addr, NewHealthStatus(), reg)
	srv.Start()
	t.Cleanup(func() { srv.srv.Close() })

	var body string
	for i := 0; i < 100; i++ {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(body, "orderexec_rate_limiter_max 9") {
		t.Fatalf("/metrics missing order gauge:\n%s", body)
	}
	if strings.Contains(body, "mdengine_") {
		t.Fatal("/metrics exports mdengine_* series")
	}
}
