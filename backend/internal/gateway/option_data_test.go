package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
)

type fakeKV struct {
	mu   sync.Mutex
	vals map[string]string
	ttls map[string]time.Duration
	pubs []string
}

func newFakeKV() *fakeKV { return &fakeKV{vals: map[string]string{}, ttls: map[string]time.Duration{}} }

func (f *fakeKV) Get(_ context.Context, k string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vals[k]
	return v, ok, nil
}
func (f *fakeKV) Set(_ context.Context, k, v string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[k], f.ttls[k] = v, ttl
	return nil
}
func (f *fakeKV) SetKeepTTL(_ context.Context, k, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[k] = v
	return nil
}
func (f *fakeKV) TTL(_ context.Context, k string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ttls[k], nil
}
func (f *fakeKV) Publish(_ context.Context, ch, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubs = append(f.pubs, ch+" "+msg)
	return nil
}

type fakeOptBroker struct {
	candleCalls, quoteCalls atomic.Int32
	lastParams              map[string]any
	mu                      sync.Mutex
	bars                    []any
}

func (b *fakeOptBroker) GetCandleData(p map[string]any) (map[string]any, error) {
	b.candleCalls.Add(1)
	b.mu.Lock()
	b.lastParams = p
	bars := b.bars
	b.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return map[string]any{"status": true, "data": bars}, nil
}
func (b *fakeOptBroker) GetMarketData(mode string, toks any) (map[string]any, error) {
	b.quoteCalls.Add(1)
	time.Sleep(20 * time.Millisecond)
	m := toks.(map[string][]string)
	var fetched []any
	for _, t := range m["NFO"] {
		fetched = append(fetched, map[string]any{"symbolToken": t, "ltp": 123.45})
	}
	return map[string]any{"status": true, "data": map[string]any{"fetched": fetched}}, nil
}

func bar(ts string, o, h, l, c float64) []any { return []any{ts, o, h, l, c, float64(100)} }

func testOptionData(kv *fakeKV, b *fakeOptBroker, now time.Time) *optionData {
	o := newOptionData(kv, func() optionBroker { return b })
	o.now = func() time.Time { return now }
	o.minGap = 0
	return o
}

func get(t *testing.T, h http.HandlerFunc, url string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func TestOptionCandles_DownloadOnceKeepOneDay(t *testing.T) {
	kv := newFakeKV()
	b := &fakeOptBroker{bars: []any{
		bar("2026-11-24T09:15:00+05:30", 100, 110, 95, 105),
		bar("2026-11-24T09:16:00+05:30", 105, 120, 104, 118),
	}}
	now := time.Date(2026, 11, 25, 20, 0, 0, 0, markethours.IST) // after hours
	o := testOptionData(kv, b, now)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, _ := get(t, o.handleCandles, "/api/options/candles?exchange=nfo&token=45123"); code != 200 {
				t.Errorf("code %d", code)
			}
		}()
	}
	wg.Wait()
	if n := b.candleCalls.Load(); n != 1 {
		t.Fatalf("%d Angel calls for 10 parallel requests", n)
	}
	key := model.OptionCandlesKeyPrefix + "NFO:45123"
	if kv.ttls[key] != 24*time.Hour {
		t.Fatalf("TTL %v, want 24h", kv.ttls[key])
	}
	if p := b.lastParams; p["exchange"] != "NFO" || p["interval"] != "ONE_MINUTE" || p["fromdate"] != "2026-11-18 20:00" {
		t.Fatalf("params %v", p)
	}
	code, m := get(t, o.handleCandles, "/api/options/candles?exchange=NFO&token=45123&tf=300")
	bars := m["bars"].([]any)
	if code != 200 || len(bars) != 1 || b.candleCalls.Load() != 1 {
		t.Fatalf("cached 5m: code %d bars %v calls %d", code, bars, b.candleCalls.Load())
	}
	b5 := bars[0].(map[string]any)
	if b5["o"].(float64) != 10000 || b5["h"].(float64) != 12000 || b5["l"].(float64) != 9500 || b5["c"].(float64) != 11800 {
		t.Fatalf("5m bar %v", b5)
	}
	if m["expires_at"] == nil {
		t.Fatal("no expires_at")
	}
}

// In market hours a stale cache fetches only the tail and keeps its TTL.
func TestOptionCandles_GapAppendKeepsTTL(t *testing.T) {
	kv := newFakeKV()
	b := &fakeOptBroker{bars: []any{bar("2026-11-25T10:00:00+05:30", 100, 101, 99, 100)}}
	t0 := time.Date(2026, 11, 25, 10, 1, 0, 0, markethours.IST)
	o := testOptionData(kv, b, t0)
	get(t, o.handleCandles, "/api/options/candles?exchange=NFO&token=1")
	key := model.OptionCandlesKeyPrefix + "NFO:1"
	kv.ttls[key] = 20 * time.Hour // as if set 4h ago

	b.bars = []any{bar("2026-11-25T10:00:00+05:30", 100, 102, 99, 101), bar("2026-11-25T10:09:00+05:30", 101, 105, 100, 104)}
	o.now = func() time.Time { return t0.Add(10 * time.Minute) }
	_, m := get(t, o.handleCandles, "/api/options/candles?exchange=NFO&token=1")
	if n := len(m["bars"].([]any)); n != 2 || b.candleCalls.Load() != 2 {
		t.Fatalf("bars %d calls %d", n, b.candleCalls.Load())
	}
	if b.lastParams["fromdate"] != "2026-11-25 10:00" {
		t.Fatalf("gap from %v", b.lastParams["fromdate"])
	}
	if kv.ttls[key] != 20*time.Hour {
		t.Fatalf("TTL reset to %v", kv.ttls[key])
	}
}

func TestOptionCandles_BadInput(t *testing.T) {
	o := testOptionData(newFakeKV(), &fakeOptBroker{}, time.Now())
	for _, u := range []string{"?exchange=NSE&token=99926000", "?exchange=NFO&token=abc", "?exchange=NFO"} {
		if code, _ := get(t, o.handleCandles, "/api/options/candles"+u); code != 400 {
			t.Errorf("%s: code %d", u, code)
		}
	}
	o.broker = func() optionBroker { return nil }
	if code, _ := get(t, o.handleCandles, "/api/options/candles?exchange=NFO&token=5"); code != 502 {
		t.Errorf("no session: code %d", code)
	}
}

func TestOptionChain_PricedOnceAndCached(t *testing.T) {
	kv := newFakeKV()
	views := map[string]model.OptChainView{"NSE:99926009": {
		Index: "NSE:99926009", Name: "BANKNIFTY", Exchange: "NFO", ATM: 57000, UpdatedAt: "u1",
		Rows: []model.OptChainRow{{Strike: 57000, CE: &model.OptChainLeg{Token: "11"}, PE: &model.OptChainLeg{Token: "12"}}},
	}}
	raw, _ := json.Marshal(views)
	kv.vals[model.OptChainContractsKey] = string(raw)
	b := &fakeOptBroker{}
	o := testOptionData(kv, b, time.Now())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(t, o.handleChain, "/api/optchain?index=NSE:99926009") }()
	}
	wg.Wait()
	if n := b.quoteCalls.Load(); n != 1 {
		t.Fatalf("%d quote calls", n)
	}
	_, m := get(t, o.handleChain, "/api/optchain?index=NSE:99926009")
	row := m["rows"].([]any)[0].(map[string]any)
	if row["ce"].(map[string]any)["ltp"].(float64) != 12345 || b.quoteCalls.Load() != 1 {
		t.Fatalf("row %v calls %d", row, b.quoteCalls.Load())
	}
	if kv.ttls[model.OptChainQuoteKeyPrefix+"NSE:99926009"] != 15*time.Second {
		t.Fatal("quote cache TTL")
	}
	if code, _ := get(t, o.handleChain, "/api/optchain?index=NSE:1"); code != 404 {
		t.Fatalf("unknown index code %d", code)
	}
	t.Setenv("INDEX_TOKEN", "NSE:99926000,NSE:99926009")
	if _, m := get(t, o.handleChain, "/api/optchain"); len(m["indexes"].([]any)) != 1 {
		t.Fatalf("index list %v", m)
	}
}

func TestOptionWatch_PublishesLease(t *testing.T) {
	kv := newFakeKV()
	o := testOptionData(kv, &fakeOptBroker{}, time.Now())
	rec := httptest.NewRecorder()
	o.handleWatch(rec, httptest.NewRequest(http.MethodPost, "/api/options/watch", strings.NewReader(`{"exchange":"BFO","token":"1163095"}`)))
	if rec.Code != 204 || len(kv.pubs) != 1 || kv.pubs[0] != model.OptionLeaseChannel+` {"exchange_type":4,"token":"1163095"}` {
		t.Fatalf("code %d pubs %v", rec.Code, kv.pubs)
	}
	rec = httptest.NewRecorder()
	o.handleWatch(rec, httptest.NewRequest(http.MethodPost, "/api/options/watch", strings.NewReader(`{"exchange":"NSE","token":"99926000"}`)))
	if rec.Code != 400 {
		t.Fatalf("index lease code %d", rec.Code)
	}
}
