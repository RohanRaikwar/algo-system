package stratengine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

func TestPublishRangeStateOnChangeOnly(t *testing.T) {
	var sent []string
	svc := &Service{nifty50RangeStrategy: strategy.NewNifty50Range(1)}
	svc.rangePublishHook = func(p string) { sent = append(sent, p) }
	ctx := context.Background()
	if last := svc.publishRangeState(ctx, ""); last != "" || len(sent) != 0 {
		t.Fatal("nothing to publish before the first candle")
	}
	ts := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	svc.nifty50RangeStrategy.OnTFCandle(model.TFCandle{Token: "99926000", Exchange: "NSE", TF: 60, TS: ts, Open: 1, High: 1, Low: 1, Close: 1})
	last := svc.publishRangeState(ctx, "")
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	var v strategy.RangeView
	if err := json.Unmarshal([]byte(sent[0]), &v); err != nil || v.Strategy != "NIFTY50_RANGE" || v.Regime != "WARMING" {
		t.Fatalf("payload %s err=%v", sent[0], err)
	}
	svc.publishRangeState(ctx, last)
	if len(sent) != 1 {
		t.Fatal("unchanged view republished")
	}
}
