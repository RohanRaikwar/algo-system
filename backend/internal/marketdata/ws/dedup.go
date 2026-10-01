package ws

import (
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

const (
	// primaryFailover: the primary socket silent (no frame at all) this long
	// makes the next socket that delivers a tick the primary. Live, a socket
	// carries hundreds of frames a second, so 1.5s of silence is a stalled
	// path.
	primaryFailover = 1500 * time.Millisecond
	// lagFailover: the standby delivered sequence number N and the primary
	// has not reached N this long after. The counter is exchange-wide and
	// identical on every socket, so this catches a primary that still
	// trickles frames but runs behind (TCP retransmit backoff).
	lagFailover = time.Second
	// switchGuard: for this long after a switch, a token's tick older than
	// the last one accepted for it (from the old primary) is dropped, so the
	// new socket cannot replay prices the old one already delivered.
	switchGuard = 5 * time.Second
)

// feedMerger turns parallel feed sockets carrying the same subscriptions into
// one tick stream: ticks pass only from the primary socket, and the primary
// moves to another socket when it stalls.
//
// First-copy-wins dedup does not work for Angel: the NIFTY index (and any
// token in LTP-style updates) carries sequence_number 0 and a 1s-resolution
// exchange time, and each socket gets its own conflated price updates within
// that second. Interleaving two such streams would reorder prices. One
// primary keeps the stream exactly as ordered as a single socket was.
type feedMerger struct {
	failover time.Duration
	guard    time.Duration
	now      func() time.Time
	// lastFrame reports when socket i last received any frame.
	lastFrame func(i int) time.Time

	lag time.Duration

	mu         sync.Mutex
	primary    int
	switchedAt time.Time
	last       map[string]mergeMark // exchange:token -> newest accepted
	hw         map[hwKey]int64      // newest seq per (socket, exchange)
	probes     map[string]lagProbe  // exchange -> standby ahead of primary
}

type hwKey struct {
	conn     int
	exchange string
}

// lagProbe: socket conn delivered seq at `at` before the primary had.
type lagProbe struct {
	conn int
	seq  int64
	at   time.Time
}

type mergeMark struct {
	seq     int64
	eventNs int64
}

func newFeedMerger(lastFrame func(i int) time.Time) *feedMerger {
	return &feedMerger{
		failover:  primaryFailover,
		guard:     switchGuard,
		now:       time.Now,
		lastFrame: lastFrame,
		lag:       lagFailover,
		last:      make(map[string]mergeMark),
		hw:        make(map[hwKey]int64),
		probes:    make(map[string]lagProbe),
	}
}

// accept reports whether tick from socket conn (feed sequence number seq, 0 if
// absent; tick.EventTS must still be the raw exchange time) passes. switched
// is the previous primary when this tick made conn the primary, else -1.
func (m *feedMerger) accept(conn int, tick *model.Tick, seq int64) (ok bool, switched int) {
	now := m.now()
	key := tick.Exchange + ":" + tick.Token
	cur := mergeMark{seq: seq}
	if !tick.EventTS.IsZero() {
		cur.eventNs = tick.EventTS.UnixNano()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switched = -1
	if seq > 0 {
		m.trackSeq(conn, tick.Exchange, seq, now)
	}
	if conn != m.primary {
		if !m.primaryFailed(conn, tick.Exchange, now) {
			return false, -1 // primary is healthy: this is a copy
		}
		switched = m.primary
		m.primary = conn
		m.switchedAt = now
		m.probes = make(map[string]lagProbe)
	}
	prev, seen := m.last[key]
	if seen && !m.switchedAt.IsZero() && now.Sub(m.switchedAt) < m.guard && older(cur, prev) {
		return false, switched
	}
	m.last[key] = cur
	return true, switched
}

// trackSeq records socket conn's newest seq on exchange and keeps the lag
// probe: opened when a standby gets ahead of the primary, closed once the
// primary catches up. Caller holds m.mu.
func (m *feedMerger) trackSeq(conn int, exchange string, seq int64, now time.Time) {
	k := hwKey{conn, exchange}
	if seq > m.hw[k] {
		m.hw[k] = seq
	}
	p, open := m.probes[exchange]
	if conn == m.primary {
		if open && seq >= p.seq {
			delete(m.probes, exchange)
		}
		return
	}
	if !open && seq > m.hw[hwKey{m.primary, exchange}] {
		m.probes[exchange] = lagProbe{conn: conn, seq: seq, at: now}
	}
}

// primaryFailed reports whether the primary must hand over to conn: silent
// for m.failover, or still short of a seq conn delivered m.lag ago. Caller
// holds m.mu.
func (m *feedMerger) primaryFailed(conn int, exchange string, now time.Time) bool {
	if lf := m.lastFrame(m.primary); lf.IsZero() || now.Sub(lf) >= m.failover {
		return true
	}
	p, open := m.probes[exchange]
	return open && p.conn == conn && now.Sub(p.at) >= m.lag
}

// older reports whether cur predates prev: a lower sequence number when both
// have one, else an earlier exchange time.
func older(cur, prev mergeMark) bool {
	if cur.seq > 0 && prev.seq > 0 {
		return cur.seq < prev.seq
	}
	return cur.eventNs < prev.eventNs
}

// Primary is the socket ticks currently come from.
func (m *feedMerger) Primary() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.primary
}
