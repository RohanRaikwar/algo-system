package gateway

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// Broadcaster constructs envelope JSON and sends filtered messages to clients.
type Broadcaster struct {
	hub *Hub
}

// NewBroadcaster creates a Broadcaster backed by the given Hub.
func NewBroadcaster(hub *Hub) *Broadcaster {
	return &Broadcaster{hub: hub}
}

// Broadcast sends data on a channel to all subscribed clients.
// Uses hand-crafted JSON envelope for performance (~1μs vs ~25μs for json.Marshal).
// Includes per-channel seq for client-side gap detection.
func (b *Broadcaster) Broadcast(channel string, data []byte) {
	now := time.Now().UTC()

	// Tick pipeline latency: mdengine receive time (tick_ts) to this send.
	// Only ticks carry a receive stamp; candle and indicator "ts" fields are
	// bucket start times, so measuring them reports the candle's age.
	if b.hub.Latency != nil && strings.HasPrefix(channel, "pub:tick:") {
		if srcTS := extractTickTS(data); !srcTS.IsZero() {
			latencyMs := float64(now.Sub(srcTS).Microseconds()) / 1000.0
			if latencyMs >= 0 {
				b.hub.Latency.Record(latencyMs)
			}
		}
	}

	b.hub.mu.Lock()

	// Per-channel seq for gap detection
	b.hub.channelSeqs[channel]++
	channelSeq := b.hub.channelSeqs[channel]
	b.hub.latest[channel] = latestEntry{Data: data, TS: now, Seq: channelSeq}
	epoch := b.hub.epoch

	// Global seq (backwards compatible)
	b.hub.seq++
	seq := b.hub.seq

	// Replay buffer for gap backfill (500 envelopes per channel). Fetched in the
	// same critical section as the seq so an idle sweep cannot slip between them.
	rb, exists := b.hub.replayBufs[channel]
	if !exists {
		rb = NewReplayBuffer(500)
		b.hub.replayBufs[channel] = rb
	}

	// Hand-craft envelope JSON
	buf := make([]byte, 0, len(channel)+len(data)+len(epoch)+172)
	buf = append(buf, `{"channel":"`...)
	buf = append(buf, channel...)
	buf = append(buf, `","data":`...)
	buf = append(buf, data...)
	buf = append(buf, `,"ts":"`...)
	buf = now.AppendFormat(buf, time.RFC3339Nano)
	buf = append(buf, `","seq":`...)
	buf = strconv.AppendInt(buf, seq, 10)
	buf = append(buf, `,"channel_seq":`...)
	buf = strconv.AppendInt(buf, channelSeq, 10)
	buf = append(buf, `,"epoch":"`...)
	buf = append(buf, epoch...)
	buf = append(buf, `"}`...)

	// Pushed inside the critical section so a reader that sees channelSeq
	// (e.g. /api/missed current_seq) also finds it in the buffer.
	rb.Push(channelSeq, buf)
	b.hub.mu.Unlock()

	// Fan out to subscribed clients. Full-state channels keep only the newest
	// frame for a client whose queue is full. Lossless event channels are never
	// silently dropped: a client whose queue cannot take one is disconnected
	// (after the read lock is released) and resyncs on reconnect.
	latestWins := isLatestWins(channel)
	lossless := losslessChannels[channel]
	var slow []*Client
	b.hub.mu.RLock()
	for client := range b.hub.clients {
		if !client.matchesChannel(channel) {
			continue
		}
		if latestWins {
			client.SendLatest(channel, buf)
			continue
		}
		if !client.SafeSend(buf) && lossless {
			slow = append(slow, client)
		}
	}
	b.hub.mu.RUnlock()
	for _, c := range slow {
		c.disconnectSlow(channel)
	}
}

// latestWinsChannels carry full state: each frame replaces the previous one,
// so a client that falls behind only needs the newest. stratengine publishes
// pub:orders on every option tick while a position is open, so disconnecting
// on overflow here would flap slow clients at tick rate.
var latestWinsChannels = map[string]bool{
	"pub:orders": true,
	"pub:pnl":    true,
	"pub:strike": true,
}

// isLatestWins also covers pub:analyst:* — each frame is the full analyst
// state for its key, so only the newest matters.
func isLatestWins(channel string) bool {
	return latestWinsChannels[channel] || strings.HasPrefix(channel, "pub:analyst:")
}

// losslessChannels carry events the dashboard must not miss (signals). Ticks
// and indicators are superseded by the next value and keep drop-on-full
// behaviour.
var losslessChannels = map[string]bool{
	"pub:signal": true,
}

var tickTSKey = []byte(`"tick_ts":"`)

// extractTickTS returns the tick_ts field of a tick payload without decoding
// the whole message (this runs for every tick on the broadcast path).
func extractTickTS(data []byte) time.Time {
	i := bytes.Index(data, tickTSKey)
	if i < 0 {
		return time.Time{}
	}
	rest := data[i+len(tickTSKey):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(rest[:j]))
	if err != nil {
		return time.Time{}
	}
	return ts
}
