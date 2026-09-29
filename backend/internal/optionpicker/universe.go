package optionpicker

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

type Resolver interface {
	Lookup(expiry time.Time, strike int64, opt string) (token, symbol string, ok bool)
	Search(expiry time.Time, strike int64, opt string) (token, symbol string, err error)
}

type Subscriber interface {
	SubscribeOptions(tokens []string)
}

type ukey struct {
	day    string // expiry date YYYY-MM-DD (IST)
	strike int64
	opt    string
}

type uval struct{ token, symbol string }

// Universe decides which option contracts stream in SnapQuote: ATM ± strikes
// on each candidate expiry. Tokens come from the offline resolver; misses are
// searched in the background at ≤ 1 per `every` (searchScrip allows 1/s).
type Universe struct {
	res     Resolver
	sub     Subscriber
	strikes int
	step    int64

	mu         sync.RWMutex
	tokens     map[ukey]uval
	subscribed map[string]bool
	atm        int64
	expKey     string
	pending    []ukey
	pendingSet map[ukey]bool
	expiries   map[string]time.Time
	curDays    map[string]bool // expiry days in the current window (for RunSearch staleness checks)
}

func NewUniverse(res Resolver, sub Subscriber, strikes int, step int64) *Universe {
	return &Universe{res: res, sub: sub, strikes: strikes, step: step,
		tokens: map[ukey]uval{}, subscribed: map[string]bool{}, pendingSet: map[ukey]bool{}, expiries: map[string]time.Time{}, curDays: map[string]bool{}}
}

// NewSession forgets what was subscribed (the feed's dynamic subscriptions
// reset each trading session) and the ladder centre, so the next Refresh
// subscribes the ladder again. The token cache is kept.
func (u *Universe) NewSession() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.subscribed = map[string]bool{}
	u.pending = nil
	u.pendingSet = map[ukey]bool{}
	u.atm = 0
	u.expKey = ""
}

func dayKey(t time.Time) string { return t.In(istZone).Format("2006-01-02") }

// Refresh rebuilds the wanted set when ATM moved ≥ 2 strikes or the expiry
// set changed, and subscribes contracts not yet subscribed.
func (u *Universe) Refresh(spot int64, expiries []time.Time) {
	if spot <= 0 || len(expiries) == 0 {
		return
	}
	atm := (spot/100 + u.step/2) / u.step * u.step
	days := make([]string, 0, len(expiries))
	for _, e := range expiries {
		days = append(days, dayKey(e))
	}
	sort.Strings(days)
	expKey := ""
	for _, d := range days {
		expKey += d + ","
	}

	u.mu.Lock()
	moved := u.atm == 0 || abs64(atm-u.atm) >= 2*u.step
	if !moved && expKey == u.expKey {
		u.mu.Unlock()
		return
	}
	u.atm, u.expKey = atm, expKey
	newDays := make(map[string]bool, len(expiries))
	for _, d := range days {
		newDays[d] = true
	}
	u.curDays = newDays
	var fresh []string
	for _, e := range expiries {
		u.expiries[dayKey(e)] = e
		for i := -u.strikes; i <= u.strikes; i++ {
			for _, opt := range [...]string{"CE", "PE"} {
				k := ukey{dayKey(e), atm + int64(i)*u.step, opt}
				v, ok := u.tokens[k]
				if !ok {
					tok, sym, found := u.res.Lookup(e, k.strike, opt)
					if !found {
						if !u.pendingSet[k] {
							u.pendingSet[k] = true
							u.pending = append(u.pending, k)
						}
						continue
					}
					v = uval{tok, sym}
					u.tokens[k] = v
				}
				if !u.subscribed[v.token] {
					u.subscribed[v.token] = true
					fresh = append(fresh, v.token)
				}
			}
		}
	}
	u.mu.Unlock()
	if len(fresh) > 0 {
		u.sub.SubscribeOptions(fresh)
		log.Printf("[optionpicker] universe around %d: +%d contracts (%d streamed)", atm, len(fresh), u.Size())
	}
}

func (u *Universe) Token(expiry time.Time, strike int64, opt string) (string, string, bool) {
	u.mu.RLock()
	v, ok := u.tokens[ukey{dayKey(expiry), strike, opt}]
	u.mu.RUnlock()
	return v.token, v.symbol, ok
}

func (u *Universe) Size() int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.subscribed)
}

// RunSearch resolves offline misses by broker search, one per `every`.
func (u *Universe) RunSearch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u.mu.Lock()
		if len(u.pending) == 0 {
			u.mu.Unlock()
			continue
		}
		k := u.pending[0]
		u.pending = u.pending[1:]
		delete(u.pendingSet, k)
		stale := abs64(k.strike-u.atm) > int64(u.strikes)*u.step || !u.curDays[k.day]
		exp := u.expiries[k.day]
		u.mu.Unlock()

		if stale {
			continue
		}

		tok, sym, err := u.res.Search(exp, k.strike, k.opt)
		if err != nil {
			log.Printf("[optionpicker] search %s %d%s: %v", k.day, k.strike, k.opt, err)
			continue
		}
		u.mu.Lock()
		u.tokens[k] = uval{tok, sym}
		isNew := !u.subscribed[tok]
		u.subscribed[tok] = true
		u.mu.Unlock()
		if isNew {
			u.sub.SubscribeOptions([]string{tok})
		}
	}
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
