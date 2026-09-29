package optionpicker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeRes struct {
	missing  map[string]bool
	searched []string
}

func (f *fakeRes) key(e time.Time, k int64, o string) string {
	return fmt.Sprintf("%s%d%s", e.Format("0102"), k, o)
}
func (f *fakeRes) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	if f.missing[f.key(e, k, o)] {
		return "", "", false
	}
	return "T" + f.key(e, k, o), "S" + f.key(e, k, o), true
}
func (f *fakeRes) Search(e time.Time, k int64, o string) (string, string, error) {
	f.searched = append(f.searched, f.key(e, k, o))
	if k == 99999 {
		return "", "", errors.New("no")
	}
	return "T" + f.key(e, k, o), "S" + f.key(e, k, o), nil
}

type fakeSub struct{ got []string }

func (f *fakeSub) SubscribeOptions(t []string) { f.got = append(f.got, t...) }

func TestUniverseSubscribesLadderOnce(t *testing.T) {
	res, sub := &fakeRes{}, &fakeSub{}
	u := NewUniverse(res, sub, 2, 50)
	u.Refresh(2270000, []time.Time{exp1})
	if len(sub.got) != 10 { // (2×2+1) strikes × CE/PE
		t.Fatalf("subscribed %d: %v", len(sub.got), sub.got)
	}
	u.Refresh(2272000, []time.Time{exp1}) // same ATM (22700): no rebuild
	if len(sub.got) != 10 {
		t.Fatalf("re-subscribed: %d", len(sub.got))
	}
	if tok, _, ok := u.Token(exp1, 22650, "PE"); !ok || tok != "T100622650PE" {
		t.Fatalf("token %q ok %v", tok, ok)
	}
}

func TestUniverseRecentresOnSpotMove(t *testing.T) {
	res, sub := &fakeRes{}, &fakeSub{}
	u := NewUniverse(res, sub, 2, 50)
	u.Refresh(2270000, []time.Time{exp1})
	u.Refresh(2280000, []time.Time{exp1}) // ATM 22800: +2 strikes → re-centre
	if _, _, ok := u.Token(exp1, 22900, "CE"); !ok {
		t.Fatal("new edge strike not in universe")
	}
	if len(sub.got) != 10+4 { // only 22850 and 22900 CE/PE are new
		t.Fatalf("subscribed %d", len(sub.got))
	}
}

func TestUniverseSearchesMissesInBackground(t *testing.T) {
	res := &fakeRes{missing: map[string]bool{"100622700CE": true}}
	sub := &fakeSub{}
	u := NewUniverse(res, sub, 0, 50)
	u.Refresh(2270000, []time.Time{exp1})
	if _, _, ok := u.Token(exp1, 22700, "CE"); ok {
		t.Fatal("miss resolved without search")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	u.RunSearch(ctx, 10*time.Millisecond)
	if tok, _, ok := u.Token(exp1, 22700, "CE"); !ok || tok != "T100622700CE" || len(res.searched) != 1 {
		t.Fatalf("tok %q ok %v searched %v", tok, ok, res.searched)
	}
}
