package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// Live indicator peeks run only for the configured tokens: option contracts
// streamed for strike selection must not get indicators on every TF.
func TestSubscribe1sForPeek_OnlyListedTokens(t *testing.T) {
	r, err := NewReader(ReaderConfig{Addr: "127.0.0.1:6379", DB: 15, ConsumerGroup: "peek-test", ConsumerName: "c1"})
	if err != nil {
		t.Skipf("no local redis: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan model.TFCandle, 16)
	go func() { _ = r.Subscribe1sForPeek(ctx, []int{60}, []string{"NSE:99926000"}, out) }()
	time.Sleep(200 * time.Millisecond) // let the subscription land

	pub := func(exch, token string) {
		b, _ := json.Marshal(model.Candle{Exchange: exch, Token: token, TS: time.Now(), Open: 1, High: 1, Low: 1, Close: 1})
		r.client.Publish(ctx, "pub:candle:1s:"+exch+":"+token, string(b))
	}
	pub("NFO", "40712")
	pub("NSE", "99926000")

	select {
	case c := <-out:
		if c.Exchange != "NSE" || c.Token != "99926000" || c.TF != 60 {
			t.Fatalf("peek for %s:%s tf=%d, want only NSE:99926000", c.Exchange, c.Token, c.TF)
		}
	case <-ctx.Done():
		t.Fatal("no peek for the listed token")
	}
	select {
	case c := <-out:
		t.Fatalf("unexpected peek for %s:%s", c.Exchange, c.Token)
	case <-time.After(300 * time.Millisecond):
	}
}
