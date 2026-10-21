package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// A phone in Doze can be out of reach for minutes; the push service must
// hold the alert that long or it is dropped for good.
func TestDeliverHoldsAlertsForSleepingPhones(t *testing.T) {
	var got *webpush.Options
	var payload []byte
	s := NewSender(Config{PublicKey: "pub", PrivateKey: "priv"}, nil)
	s.send = func(_ context.Context, msg []byte, _ *webpush.Subscription, o *webpush.Options) (*http.Response, error) {
		got, payload = o, msg
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	sub, _ := json.Marshal(webpush.Subscription{Endpoint: "https://fcm.googleapis.com/x", Keys: webpush.Keys{Auth: "a", P256dh: "p"}})
	n, err := s.deliver(context.Background(), map[string]string{"https://fcm.googleapis.com/x": string(sub)}, Notification{Title: "EXIT", TS: 1700000000000})
	if err != nil || n != 1 {
		t.Fatalf("delivered %d, err %v", n, err)
	}
	if got.TTL < 10*60 {
		t.Errorf("TTL %ds: an alert for a sleeping phone expires before it wakes", got.TTL)
	}
	if got.Urgency != webpush.UrgencyHigh {
		t.Errorf("urgency %q, want high", got.Urgency)
	}
	if !strings.Contains(string(payload), `"ts":1700000000000`) {
		t.Errorf("payload %s has no event time", payload)
	}
}

func TestEnqueueStampsEventTime(t *testing.T) {
	svc := NewService(nil, nil)
	svc.enqueue(Notification{Title: "BUY"})
	if n := <-svc.queue; n.TS == 0 {
		t.Fatal("no event time")
	}
}
