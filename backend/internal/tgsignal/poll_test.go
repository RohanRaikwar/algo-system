package tgsignal

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type fakeDiff struct {
	res  []tg.UpdatesChannelDifferenceClass
	err  error
	reqs []tg.UpdatesGetChannelDifferenceRequest
}

func (f *fakeDiff) UpdatesGetChannelDifference(_ context.Context, req *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	f.reqs = append(f.reqs, *req)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.res) == 0 {
		return &tg.UpdatesChannelDifferenceEmpty{Pts: req.Pts, Final: true}, nil
	}
	r := f.res[0]
	f.res = f.res[1:]
	return r, nil
}

type delivered struct {
	id     int
	edited bool
}

func chanMsg(id int, text string, date time.Time) *tg.Message {
	return &tg.Message{ID: id, Message: text, Date: int(date.Unix()), PeerID: &tg.PeerChannel{ChannelID: 42}}
}

func newTestPoller(api diffAPI) (*channelPoller, *[]delivered) {
	var got []delivered
	p := &channelPoller{api: api, limit: 100, chans: []*polledChannel{{id: 42, hash: 7, pts: 100}},
		deliver: func(m tg.MessageClass, edited bool) {
			got = append(got, delivered{m.(*tg.Message).ID, edited})
		}}
	return p, &got
}

func TestPollerDeliversNewAndEditsNotDeletes(t *testing.T) {
	api := &fakeDiff{res: []tg.UpdatesChannelDifferenceClass{
		&tg.UpdatesChannelDifference{Pts: 105, Final: false,
			NewMessages: []tg.MessageClass{chanMsg(10, "a", tNow)},
			OtherUpdates: []tg.UpdateClass{
				&tg.UpdateEditChannelMessage{Message: chanMsg(9, "b", tNow)},
				&tg.UpdateDeleteChannelMessages{ChannelID: 42, Messages: []int{8}},
			}},
		&tg.UpdatesChannelDifference{Pts: 106, Final: true, NewMessages: []tg.MessageClass{chanMsg(11, "c", tNow)}},
	}}
	p, got := newTestPoller(api)
	if wait, err := p.pollOnce(context.Background()); err != nil || wait != 0 {
		t.Fatalf("pollOnce: %v %v", wait, err)
	}
	want := []delivered{{10, false}, {9, true}, {11, false}}
	if len(*got) != len(want) {
		t.Fatalf("delivered %+v", *got)
	}
	for i := range want {
		if (*got)[i] != want[i] {
			t.Fatalf("delivered %+v, want %+v", *got, want)
		}
	}
	if len(api.reqs) != 2 || api.reqs[0].Pts != 100 || api.reqs[1].Pts != 105 {
		t.Fatalf("requests %+v", api.reqs)
	}
	ch, ok := api.reqs[0].Channel.(*tg.InputChannel)
	if !ok || ch.ChannelID != 42 || ch.AccessHash != 7 {
		t.Fatalf("channel %+v", api.reqs[0].Channel)
	}
	if p.chans[0].pts != 106 {
		t.Fatalf("pts %d", p.chans[0].pts)
	}
	// Next tick asks from the new pts.
	_, _ = p.pollOnce(context.Background())
	if api.reqs[2].Pts != 106 {
		t.Fatalf("next pts %d", api.reqs[2].Pts)
	}
}

func TestPollerTooLongResetsPts(t *testing.T) {
	d := &tg.Dialog{}
	d.SetPts(500)
	api := &fakeDiff{res: []tg.UpdatesChannelDifferenceClass{
		&tg.UpdatesChannelDifferenceTooLong{Dialog: d, Messages: []tg.MessageClass{chanMsg(20, "x", tNow)}},
	}}
	p, got := newTestPoller(api)
	if _, err := p.pollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.chans[0].pts != 500 || len(*got) != 1 || (*got)[0].id != 20 {
		t.Fatalf("pts %d delivered %+v", p.chans[0].pts, *got)
	}
}

func TestPollerFloodWait(t *testing.T) {
	api := &fakeDiff{err: tgerr.New(420, "FLOOD_WAIT_7")}
	p, _ := newTestPoller(api)
	wait, err := p.pollOnce(context.Background())
	if err == nil || wait != 7*time.Second {
		t.Fatalf("wait %v err %v", wait, err)
	}
	if p.chans[0].pts != 100 {
		t.Fatalf("pts moved on error: %d", p.chans[0].pts)
	}
}

func TestPollInterval(t *testing.T) {
	c := Config{PollInterval: 2 * time.Second, PollIdle: 30 * time.Second}
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, istZone)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	cases := map[string]time.Duration{
		"2026-11-26 08:44": 30 * time.Second,
		"2026-11-26 08:45": 2 * time.Second, // Thursday
		"2026-11-26 15:34": 2 * time.Second,
		"2026-11-26 15:35": 30 * time.Second,
		"2026-11-28 10:00": 30 * time.Second, // Saturday
	}
	for s, want := range cases {
		if got := c.pollInterval(at(s)); got != want {
			t.Errorf("%s: %v, want %v", s, got, want)
		}
	}
	c.PollIdle = 0 // never faster outside hours than inside
	if got := c.pollInterval(at("2026-11-28 10:00")); got != 2*time.Second {
		t.Errorf("idle 0: %v", got)
	}
}

func TestDeliveriesNote(t *testing.T) {
	var d deliveries
	if _, ok := d.note("k", srcPoll, tNow); ok {
		t.Fatal("first delivery reported")
	}
	if _, ok := d.note("k", srcPoll, tNow.Add(time.Second)); ok {
		t.Fatal("same source reported")
	}
	f, ok := d.note("k", srcPush, tNow.Add(3*time.Second))
	if !ok || f.src != srcPoll || !f.at.Equal(tNow) {
		t.Fatalf("got %+v %v", f, ok)
	}
	if _, ok := d.note("k", srcPush, tNow.Add(4*time.Second)); ok {
		t.Fatal("reported twice")
	}
}

// A call that comes by poll and again by push is published once.
func TestPushAndPollPublishOnce(t *testing.T) {
	h, s, _ := newTestHandler(t, nil)
	svc := &Service{h: h}
	now := time.Now()
	m := chanMsg(5, realCall, now)
	svc.onPolled(m, false)
	svc.onMessage(context.Background(), m, false)
	h.r.waitIdle()
	if len(s.calls) != 1 || s.calls[0].MsgID != 5 {
		t.Fatalf("calls %+v", s.calls)
	}
	// Other chats are not delivered at all.
	other := chanMsg(6, realCall, now)
	other.PeerID = &tg.PeerChannel{ChannelID: 7}
	svc.onPolled(other, false)
	h.r.waitIdle()
	if len(s.calls) != 1 {
		t.Fatalf("other chat published: %+v", s.calls)
	}
}
