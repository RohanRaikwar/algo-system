package stratengine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

func TestPublishSRStateOnChangeOnly(t *testing.T) {
	var sent []string
	svc := &Service{srStrategy: strategy.NewNifty50SR(1, strategy.DefaultNifty50SRConfig())}
	svc.srPublishHook = func(p string) { sent = append(sent, p) }
	ctx := context.Background()
	if last := svc.publishSRState(ctx, ""); last != "" || len(sent) != 0 {
		t.Fatal("nothing to publish before the first candle")
	}
	ts := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	svc.srStrategy.OnTFCandle(model.TFCandle{Token: "99926000", Exchange: "NSE", TF: 60, TS: ts, Open: 1, High: 1, Low: 1, Close: 1})
	last := svc.publishSRState(ctx, "")
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	var v strategy.SRView
	if err := json.Unmarshal([]byte(sent[0]), &v); err != nil || v.Strategy != "NIFTY50_SR" || v.Regime != "WARMING" {
		t.Fatalf("payload %s err=%v", sent[0], err)
	}
	svc.publishSRState(ctx, last)
	if len(sent) != 1 {
		t.Fatal("unchanged view republished")
	}
}
