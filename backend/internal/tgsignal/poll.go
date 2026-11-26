package tgsignal

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// Telegram does not push every update of a large channel to a user
// account: official apps poll channels.getChannelDifference while a
// channel is open. gotd's update manager asks only after a gap or 15
// minutes idle, so a call could reach us minutes late. The poller asks
// every TG_POLL_INTERVAL for each followed channel, with its own pts,
// and hands what it finds to the same onMessage path; the router's
// firstSeen drops whichever copy (push or poll) comes second.

// diffAPI is the one Telegram call the poller makes per tick.
type diffAPI interface {
	UpdatesGetChannelDifference(ctx context.Context, req *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error)
}

// polledChannel is one followed channel and the pts we have read to.
type polledChannel struct {
	id   int64
	hash int64
	pts  int
}

// channelPoller fetches each channel's difference and delivers messages.
type channelPoller struct {
	api     diffAPI
	chans   []*polledChannel
	deliver func(m tg.MessageClass, edited bool)
	limit   int
}

// pollOnce reads every channel's new messages and edits. It returns the
// longest FLOOD_WAIT seen (0 = none) so the caller can back off.
func (p *channelPoller) pollOnce(ctx context.Context) (time.Duration, error) {
	var wait time.Duration
	var firstErr error
	for _, c := range p.chans {
		d, err := p.pollChannel(ctx, c)
		if d > wait {
			wait = d
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("channel %d: %w", c.id, err)
		}
	}
	return wait, firstErr
}

// pollChannel drains one channel's difference (several calls while the
// server says it is not final, at most 5).
func (p *channelPoller) pollChannel(ctx context.Context, c *polledChannel) (time.Duration, error) {
	for range 5 {
		res, err := p.api.UpdatesGetChannelDifference(ctx, &tg.UpdatesGetChannelDifferenceRequest{
			Channel: &tg.InputChannel{ChannelID: c.id, AccessHash: c.hash},
			Filter:  &tg.ChannelMessagesFilterEmpty{},
			Pts:     c.pts,
			Limit:   p.limit,
		})
		if err != nil {
			if d, ok := tgerr.AsFloodWait(err); ok {
				return d, err
			}
			return 0, err
		}
		final := true
		switch r := res.(type) {
		case *tg.UpdatesChannelDifferenceEmpty:
			c.pts = r.Pts
		case *tg.UpdatesChannelDifference:
			for _, m := range r.NewMessages {
				p.deliver(m, false)
			}
			for _, u := range r.OtherUpdates {
				switch u := u.(type) {
				case *tg.UpdateNewChannelMessage:
					p.deliver(u.Message, false)
				case *tg.UpdateEditChannelMessage:
					p.deliver(u.Message, true)
				}
				// Deletes stay with the push path: a second delete would
				// withdraw a call twice.
			}
			c.pts, final = r.Pts, r.Final
		case *tg.UpdatesChannelDifferenceTooLong:
			// Too far behind: restart from the dialog's pts. The latest
			// messages come along; maxAge drops old ones.
			if d, ok := r.Dialog.(*tg.Dialog); ok {
				if pts, ok := d.GetPts(); ok {
					c.pts = pts
				}
			}
			for _, m := range r.Messages {
				p.deliver(m, false)
			}
		}
		if final {
			return 0, nil
		}
	}
	return 0, nil
}

// findChannels scans the dialogs once for the followed channels' access
// hashes and current pts. Basic groups (not channels) are not polled:
// their updates come on the account's common sequence, which is pushed.
func findChannels(ctx context.Context, api *tg.Client, ids []int64) ([]*polledChannel, error) {
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []*polledChannel
	err := dialogs.NewQueryBuilder(api).GetDialogs().BatchSize(100).ForEach(ctx, func(_ context.Context, e dialogs.Elem) error {
		p, ok := e.Peer.(*tg.InputPeerChannel)
		if !ok || !want[p.ChannelID] {
			return nil
		}
		c := &polledChannel{id: p.ChannelID, hash: p.AccessHash}
		if d, ok := e.Dialog.(*tg.Dialog); ok {
			c.pts, _ = d.GetPts()
		}
		delete(want, p.ChannelID)
		out = append(out, c)
		if len(want) == 0 {
			return errFoundAll
		}
		return nil
	})
	if err == errFoundAll {
		err = nil
	}
	return out, err
}

var errFoundAll = fmt.Errorf("all channels found")

// pollInterval is the poll period now: TG_POLL_INTERVAL in market
// hours (08:45–15:35 IST, Mon–Fri), TG_POLL_IDLE outside them.
func (c Config) pollInterval(now time.Time) time.Duration {
	ist := now.In(istZone)
	if wd := ist.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return c.idle()
	}
	mins := ist.Hour()*60 + ist.Minute()
	if mins >= 8*60+45 && mins < 15*60+35 {
		return c.PollInterval
	}
	return c.idle()
}

func (c Config) idle() time.Duration {
	if c.PollIdle < c.PollInterval {
		return c.PollInterval
	}
	return c.PollIdle
}

var istZone = time.FixedZone("IST", 5*3600+1800)

// runPoller polls until ctx ends. Errors are logged and retried; a
// FLOOD_WAIT pauses polling for the time Telegram asks.
func (svc *Service) runPoller(ctx context.Context, api *tg.Client) {
	if svc.cfg.PollInterval <= 0 {
		log.Printf("[tgsignal] channel poll off (TG_POLL_INTERVAL=0): push updates only")
		return
	}
	var chans []*polledChannel
	for ctx.Err() == nil {
		var err error
		if chans, err = findChannels(ctx, api, svc.cfg.ChatIDs); err == nil {
			break
		}
		log.Printf("[tgsignal] ⚠️ channel poll: dialog scan: %v (retry in 30s)", err)
		if !sleepCtx(ctx, 30*time.Second) {
			return
		}
	}
	if len(chans) == 0 {
		log.Printf("[tgsignal] channel poll: none of %v is a channel in the dialogs; push updates only", svc.cfg.ChatIDs)
		return
	}
	ids := make([]int64, len(chans))
	for i, c := range chans {
		ids[i] = c.id
	}
	log.Printf("[tgsignal] 🔁 channel poll on for %v: every %s in market hours, %s outside", ids, svc.cfg.PollInterval, svc.cfg.idle())
	p := &channelPoller{api: api, chans: chans, limit: 100,
		deliver: func(m tg.MessageClass, edited bool) { svc.onPolled(m, edited) }}
	lastErr := time.Time{}
	for ctx.Err() == nil {
		wait, err := p.pollOnce(ctx)
		if err != nil && ctx.Err() == nil && time.Since(lastErr) > time.Minute {
			lastErr = time.Now()
			log.Printf("[tgsignal] ⚠️ channel poll: %v", err)
		}
		if wait > 0 {
			log.Printf("[tgsignal] ⚠️ channel poll: FLOOD_WAIT %s, pausing", wait)
		}
		if !sleepCtx(ctx, max(wait, svc.cfg.pollInterval(time.Now()))) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Delivery source tracking: which path (push or poll) brought a message
// first, so the logs show what polling saves.
const (
	srcPush = "push"
	srcPoll = "poll"
)

type firstDelivery struct {
	src string
	at  time.Time
}

type deliveries struct {
	mu     sync.Mutex
	first  map[string]firstDelivery
	pruned time.Time
}

// note records a delivery. It returns the first delivery when this one
// is the second, from the other source (ok=false otherwise).
func (d *deliveries) note(key, src string, now time.Time) (firstDelivery, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.first == nil {
		d.first = make(map[string]firstDelivery)
	}
	if now.Sub(d.pruned) > time.Minute {
		d.pruned = now
		for k, f := range d.first {
			if now.Sub(f.at) > 30*time.Minute {
				delete(d.first, k)
			}
		}
	}
	f, seen := d.first[key]
	if !seen {
		d.first[key] = firstDelivery{src: src, at: now}
		return firstDelivery{}, false
	}
	if f.src == src || f.src == "" {
		return firstDelivery{}, false
	}
	d.first[key] = firstDelivery{at: f.at} // report once
	return f, true
}
