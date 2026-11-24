package stratengine

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Report work runs off the caller, in order; a slow write does not hold
// up the signal loop.
func TestReportRunsOffLoopInOrder(t *testing.T) {
	svc := &Service{reportCh: make(chan func(), reportQueueDepth)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.reportLoop(ctx)

	var mu sync.Mutex
	var got []int
	done := make(chan struct{})
	start := time.Now()
	for i := 0; i < 5; i++ {
		i := i
		svc.report(ctx, func() {
			time.Sleep(20 * time.Millisecond) // a slow Redis write
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
			if i == 4 {
				close(done)
			}
		})
	}
	if d := time.Since(start); d > 10*time.Millisecond {
		t.Fatalf("report blocked the caller for %s", d)
	}
	<-done
	for i, v := range got {
		if v != i {
			t.Fatalf("order %v", got)
		}
	}
}
