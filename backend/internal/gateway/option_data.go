package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/config"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/underlying"
)

// ════════════════════════════════════════════════════════════════════
//  Option chain and on-click option charts.
//
//  /api/optchain           the chain stratengine listed (optchain:contracts)
//                          priced by one broker quote call, cached briefly.
//  /api/options/candles    one option's last OPTION_HISTORY_DAYS of 1m bars,
//                          fetched from Angel on first request and kept in
//                          Redis OPTION_HISTORY_TTL (1 day), then gone.
//  /api/options/watch      renews the live lease of a charted option
//                          (cmd:option_lease); mdengine drops the feed a
//                          minute after the last renewal.
//  Nothing is subscribed for the chain itself and nothing reaches disk.
// ════════════════════════════════════════════════════════════════════

// optionBroker is the part of Angel's API these handlers use.
type optionBroker interface {
	GetCandleData(params map[string]any) (map[string]any, error)
	GetMarketData(mode string, exchangeTokens any) (map[string]any, error)
}

// optKV is the Redis subset the handlers use (a fake in tests).
type optKV interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, val string, ttl time.Duration) error
	SetKeepTTL(ctx context.Context, key, val string) error
	TTL(ctx context.Context, key string) (time.Duration, error)
	Publish(ctx context.Context, channel, msg string) error
}

type redisKV struct{ c *goredis.Client }

func (r redisKV) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := r.c.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}
func (r redisKV) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	return r.c.Set(ctx, key, val, ttl).Err()
}
func (r redisKV) SetKeepTTL(ctx context.Context, key, val string) error {
	return r.c.Set(ctx, key, val, goredis.KeepTTL).Err()
}
func (r redisKV) TTL(ctx context.Context, key string) (time.Duration, error) {
	return r.c.TTL(ctx, key).Result()
}
func (r redisKV) Publish(ctx context.Context, channel, msg string) error {
	return r.c.Publish(ctx, channel, msg).Err()
}

// optionData serves the option chain and option chart endpoints.
type optionData struct {
	kv       optKV
	broker   func() optionBroker // nil result: no Angel session
	now      func() time.Time
	days     int
	ttl      time.Duration
	quoteTTL time.Duration
	gapAfter time.Duration // refetch the tail when the newest bar is older

	flightMu sync.Mutex
	flights  map[string]*flight

	callMu   sync.Mutex
	lastCall time.Time
	minGap   time.Duration // between Angel calls (historical API allows 3/s)
}

type flight struct {
	done chan struct{}
	body []byte
	code int
}

func newOptionData(kv optKV, broker func() optionBroker) *optionData {
	ttl, err := time.ParseDuration(config.GetEnv("OPTION_HISTORY_TTL", "24h"))
	if err != nil || ttl <= 0 {
		ttl = 24 * time.Hour
	}
	days := config.GetEnvInt("OPTION_HISTORY_DAYS", 7)
	if days < 1 || days > 30 {
		days = 7
	}
	return &optionData{kv: kv, broker: broker, now: time.Now, days: days, ttl: ttl,
		quoteTTL: 15 * time.Second, gapAfter: 2 * time.Minute, minGap: 400 * time.Millisecond,
		flights: map[string]*flight{}}
}

// once runs fn for key with concurrent callers sharing its result.
func (o *optionData) once(key string, fn func() ([]byte, int)) ([]byte, int) {
	o.flightMu.Lock()
	if f, ok := o.flights[key]; ok {
		o.flightMu.Unlock()
		<-f.done
		return f.body, f.code
	}
	f := &flight{done: make(chan struct{})}
	o.flights[key] = f
	o.flightMu.Unlock()
	f.body, f.code = fn()
	o.flightMu.Lock()
	delete(o.flights, key)
	o.flightMu.Unlock()
	close(f.done)
	return f.body, f.code
}

// pace spaces broker calls by minGap.
func (o *optionData) pace() {
	o.callMu.Lock()
	defer o.callMu.Unlock()
	if wait := o.minGap - time.Since(o.lastCall); wait > 0 {
		time.Sleep(wait)
	}
	o.lastCall = time.Now()
}

func jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func errBody(msg string) []byte { return jsonBody(map[string]string{"error": msg}) }

func writeRaw(w http.ResponseWriter, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
}

// ── chain ───────────────────────────────────────────────────────────

func (o *optionData) contracts(ctx context.Context) (map[string]model.OptChainView, error) {
	raw, ok, err := o.kv.Get(ctx, model.OptChainContractsKey)
	if err != nil || !ok {
		return nil, err
	}
	var m map[string]model.OptChainView
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (o *optionData) handleChain(w http.ResponseWriter, r *http.Request) {
	SetCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	ctx := r.Context()
	index := strings.TrimSpace(r.URL.Query().Get("index"))
	views, err := o.contracts(ctx)
	if err != nil {
		writeRaw(w, http.StatusBadGateway, errBody("option chain unavailable"))
		return
	}
	if index == "" {
		type idx struct {
			Index  string `json:"index"`
			Name   string `json:"name"`
			Expiry string `json:"expiry"`
		}
		out := []idx{}
		for _, p := range underlying.All() {
			if v, ok := views[p.IndexKey()]; ok {
				out = append(out, idx{v.Index, v.Name, v.Expiry})
			}
		}
		writeRaw(w, http.StatusOK, jsonBody(map[string]any{"indexes": out}))
		return
	}
	v, ok := views[index]
	if !ok {
		writeRaw(w, http.StatusNotFound, errBody("no chain for "+index))
		return
	}
	qkey := model.OptChainQuoteKeyPrefix + index
	if cached, ok, _ := o.kv.Get(ctx, qkey); ok {
		var c model.OptChainView
		if json.Unmarshal([]byte(cached), &c) == nil && c.UpdatedAt == v.UpdatedAt {
			writeRaw(w, http.StatusOK, []byte(cached))
			return
		}
	}
	body, code := o.once("chain:"+index, func() ([]byte, int) {
		o.price(&v)
		b := jsonBody(v)
		if v.PriceNote == "" {
			if err := o.kv.Set(context.Background(), qkey, string(b), o.quoteTTL); err != nil {
				log.Printf("[gateway] optchain cache: %v", err)
			}
		}
		return b, http.StatusOK
	})
	writeRaw(w, code, body)
}

// price fills LTPs with one quote call (Angel takes up to 50 tokens).
func (o *optionData) price(v *model.OptChainView) {
	var api optionBroker
	if o.broker != nil {
		api = o.broker()
	}
	if api == nil {
		v.PriceNote = "no broker session"
		return
	}
	var tokens []string
	legs := map[string]*model.OptChainLeg{}
	for i := range v.Rows {
		for _, l := range []*model.OptChainLeg{v.Rows[i].CE, v.Rows[i].PE} {
			if l != nil && len(tokens) < 50 {
				tokens = append(tokens, l.Token)
				legs[l.Token] = l
			}
		}
	}
	o.pace()
	res, err := api.GetMarketData("LTP", map[string][]string{v.Exchange: tokens})
	if err != nil {
		v.PriceNote = "quote failed: " + err.Error()
		return
	}
	data, _ := res["data"].(map[string]any)
	fetched, _ := data["fetched"].([]any)
	for _, f := range fetched {
		m, _ := f.(map[string]any)
		tok, _ := m["symbolToken"].(string)
		if l := legs[tok]; l != nil {
			l.LTP = anyToPaise(m["ltp"])
		}
	}
	v.PricedAt = o.now().UTC().Format(time.RFC3339)
}

func anyToPaise(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(math.Round(t * 100))
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return int64(math.Round(f * 100))
		}
	}
	return 0
}

// ── option history ─────────────────────────────────────────────────

// optBar is one 1m bar, prices in paise.
type optBar struct {
	T int64 `json:"t"` // unix seconds, bar start
	O int64 `json:"o"`
	H int64 `json:"h"`
	L int64 `json:"l"`
	C int64 `json:"c"`
	V int64 `json:"v"`
}

type optHistory struct {
	FetchedAt int64    `json:"fetched_at"`
	Bars      []optBar `json:"bars"`
}

// optionSegments are Angel's option segments and their WS exchange types.
var optionSegments = map[string]int{"NFO": 2, "BFO": 4}

// parseOptionSegment validates exchange/token: an options segment and a
// numeric token.
func parseOptionSegment(exchange, token string) (string, string, int, error) {
	exchange = strings.ToUpper(strings.TrimSpace(exchange))
	token = strings.TrimSpace(token)
	typ, ok := optionSegments[exchange]
	if !ok {
		return "", "", 0, fmt.Errorf("exchange %q is not an options segment", exchange)
	}
	if _, err := strconv.ParseUint(token, 10, 64); err != nil || token == "" {
		return "", "", 0, fmt.Errorf("token %q is not numeric", token)
	}
	return exchange, token, typ, nil
}

func (o *optionData) handleCandles(w http.ResponseWriter, r *http.Request) {
	SetCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	q := r.URL.Query()
	exch, tok, _, err := parseOptionSegment(q.Get("exchange"), q.Get("token"))
	if err != nil {
		writeRaw(w, http.StatusBadRequest, errBody(err.Error()))
		return
	}
	tf, _ := strconv.ParseInt(q.Get("tf"), 10, 64)
	if tf <= 0 || tf%60 != 0 {
		tf = 60
	}
	key := model.OptionCandlesKeyPrefix + exch + ":" + tok
	body, code := o.once(key, func() ([]byte, int) { return o.history(exch, tok, key) })
	if code != http.StatusOK {
		writeRaw(w, code, body)
		return
	}
	var h optHistory
	_ = json.Unmarshal(body, &h)
	ttl, _ := o.kv.TTL(r.Context(), key)
	resp := map[string]any{"exchange": exch, "token": tok, "tf": tf, "bars": aggregateBars(h.Bars, tf)}
	if ttl > 0 {
		resp["expires_at"] = o.now().Add(ttl).UTC().Format(time.RFC3339)
	}
	writeRaw(w, http.StatusOK, jsonBody(resp))
}

// history returns the stored history, downloading it (or its missing tail)
// when needed.
func (o *optionData) history(exch, tok, key string) ([]byte, int) {
	ctx := context.Background()
	now := o.now()
	var h optHistory
	raw, have, err := o.kv.Get(ctx, key)
	if err != nil {
		return errBody("cache unavailable"), http.StatusBadGateway
	}
	if have && json.Unmarshal([]byte(raw), &h) != nil {
		have = false
	}
	from := now.AddDate(0, 0, -o.days)
	if have {
		stale := len(h.Bars) == 0 || now.Sub(time.Unix(h.Bars[len(h.Bars)-1].T, 0)) > o.gapAfter
		recent := now.Sub(time.Unix(h.FetchedAt, 0)) < time.Minute
		if !stale || recent || !markethours.IsMarketOpen(now) {
			return []byte(raw), http.StatusOK
		}
		if n := len(h.Bars); n > 0 {
			from = time.Unix(h.Bars[n-1].T, 0)
		}
	}
	bars, err := o.fetchBars(exch, tok, from, now)
	if err != nil {
		if have {
			return []byte(raw), http.StatusOK // keep serving what we have
		}
		return errBody(err.Error()), http.StatusBadGateway
	}
	h.Bars = mergeBars(h.Bars, bars)
	h.FetchedAt = now.Unix()
	b := jsonBody(h)
	if have {
		err = o.kv.SetKeepTTL(ctx, key, string(b)) // gone one day after the first download
	} else {
		err = o.kv.Set(ctx, key, string(b), o.ttl)
		log.Printf("[gateway] 📥 option history %s:%s: %d bars, kept %s", exch, tok, len(h.Bars), o.ttl)
	}
	if err != nil {
		log.Printf("[gateway] option history store %s: %v", key, err)
	}
	return b, http.StatusOK
}

func (o *optionData) fetchBars(exch, tok string, from, to time.Time) ([]optBar, error) {
	var api optionBroker
	if o.broker != nil {
		api = o.broker()
	}
	if api == nil {
		return nil, errors.New("no broker session")
	}
	o.pace()
	res, err := api.GetCandleData(map[string]any{
		"exchange": exch, "symboltoken": tok, "interval": "ONE_MINUTE",
		"fromdate": from.In(markethours.IST).Format("2006-01-02 15:04"),
		"todate":   to.In(markethours.IST).Format("2006-01-02 15:04"),
	})
	if err != nil {
		return nil, err
	}
	if st, ok := res["status"].(bool); ok && !st {
		return nil, fmt.Errorf("candle data: %v", res["message"])
	}
	return parseOptBars(res), nil
}

// parseOptBars reads Angel's [[ts, o, h, l, c, v], …] (rupees → paise).
func parseOptBars(res map[string]any) []optBar {
	rows, _ := res["data"].([]any)
	out := make([]optBar, 0, len(rows))
	for _, r := range rows {
		a, ok := r.([]any)
		if !ok || len(a) < 6 {
			continue
		}
		s, _ := a[0].(string)
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			continue
		}
		vol, _ := a[5].(float64)
		b := optBar{T: t.Unix(), O: anyToPaise(a[1]), H: anyToPaise(a[2]), L: anyToPaise(a[3]), C: anyToPaise(a[4]), V: int64(vol)}
		if b.O > 0 && b.C > 0 {
			out = append(out, b)
		}
	}
	return out
}

// mergeBars merges add into old by bar start (add wins), sorted.
func mergeBars(old, add []optBar) []optBar {
	m := make(map[int64]optBar, len(old)+len(add))
	for _, b := range old {
		m[b.T] = b
	}
	for _, b := range add {
		m[b.T] = b
	}
	out := make([]optBar, 0, len(m))
	for _, b := range m {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// aggregateBars builds tf-second bars from 1m bars with model.TFBucket.
func aggregateBars(bars []optBar, tf int64) []optBar {
	if tf <= 60 {
		return bars
	}
	var out []optBar
	for _, b := range bars {
		start := model.TFBucket(b.T, tf)
		if n := len(out); n > 0 && out[n-1].T == start {
			cur := &out[n-1]
			cur.H, cur.L = max(cur.H, b.H), min(cur.L, b.L)
			cur.C, cur.V = b.C, cur.V+b.V
			continue
		}
		out = append(out, optBar{T: start, O: b.O, H: b.H, L: b.L, C: b.C, V: b.V})
	}
	return out
}

// ── live lease ─────────────────────────────────────────────────────

func (o *optionData) handleWatch(w http.ResponseWriter, r *http.Request) {
	SetCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		writeRaw(w, http.StatusMethodNotAllowed, errBody("POST only"))
		return
	}
	var req struct {
		Exchange string `json:"exchange"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeRaw(w, http.StatusBadRequest, errBody("bad json"))
		return
	}
	_, tok, typ, err := parseOptionSegment(req.Exchange, req.Token)
	if err != nil {
		writeRaw(w, http.StatusBadRequest, errBody(err.Error()))
		return
	}
	msg := jsonBody(map[string]any{"exchange_type": typ, "token": tok})
	if err := o.kv.Publish(r.Context(), model.OptionLeaseChannel, string(msg)); err != nil {
		writeRaw(w, http.StatusBadGateway, errBody("lease not sent"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// registerOptionRoutes mounts the option chain and chart endpoints.
func registerOptionRoutes(mux *http.ServeMux, rdb *goredis.Client, accountSvc *AccountService) {
	o := newOptionData(redisKV{rdb}, func() optionBroker {
		if accountSvc == nil {
			return nil
		}
		return accountSvc.optionBroker()
	})
	mux.HandleFunc("/api/optchain", o.handleChain)
	mux.HandleFunc("/api/options/candles", o.handleCandles)
	mux.HandleFunc("/api/options/watch", o.handleWatch)
}
