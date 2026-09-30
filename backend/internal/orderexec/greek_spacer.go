package orderexec

import (
	"sync"
	"time"
)

// Angel One rejects an OptionGreek call made within about a second of the
// previous one ("Access denied because of exceeding access rate"). A chain
// load asks for two expiries back to back, so without spacing the second
// expiry failed almost every time and the whole load was thrown away.
const optionGreekGap = 1100 * time.Millisecond

// greekSpacer keeps OptionGreek calls at least gap apart. It is shared by
// every StrikePicker in the process because they all use one Angel account.
type greekSpacer struct {
	mu    sync.Mutex
	gap   time.Duration
	last  time.Time
	now   func() time.Time
	sleep func(time.Duration)
}

var optionGreekSpacer = &greekSpacer{gap: optionGreekGap, now: time.Now, sleep: time.Sleep}

// wait blocks until gap has passed since the previous call, then records
// this call.
func (s *greekSpacer) wait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.IsZero() {
		if d := s.gap - s.now().Sub(s.last); d > 0 {
			s.sleep(d)
		}
	}
	s.last = s.now()
}
