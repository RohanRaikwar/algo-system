package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	goredis "github.com/go-redis/redis/v8"
)

// subsKey is the Redis hash of browser subscriptions: field = endpoint URL,
// value = the PushSubscription JSON the browser produced.
const subsKey = "push:subs"

const (
	sendTimeout = 10 * time.Second
	pushTTL     = 60 // seconds: a trade alert older than this is noise
	maxInFlight = 4  // concurrent sends to push services
)

// ErrDisabled is returned when VAPID keys are not configured.
var ErrDisabled = errors.New("push disabled: VAPID keys not configured")

// ErrNotSubscribed is returned by SendTo for an endpoint that is not stored.
var ErrNotSubscribed = errors.New("this device is not subscribed")

// Config holds VAPID settings read from the environment.
type Config struct {
	PublicKey  string
	PrivateKey string
	Subject    string // mailto: or https: contact for the push service
}

// ConfigFromEnv reads VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY and VAPID_SUBJECT.
func ConfigFromEnv() Config {
	return Config{
		PublicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		PrivateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		Subject:    os.Getenv("VAPID_SUBJECT"),
	}
}

// Enabled reports whether both VAPID keys are set.
func (c Config) Enabled() bool { return c.PublicKey != "" && c.PrivateKey != "" }

// Sender stores subscriptions in Redis and delivers notifications to them.
type Sender struct {
	cfg  Config
	rdb  *goredis.Client
	send func(ctx context.Context, msg []byte, s *webpush.Subscription, o *webpush.Options) (*http.Response, error)
}

// NewSender builds a Sender. A Sender with a disabled Config still answers
// PublicKey and returns ErrDisabled from every other call.
func NewSender(cfg Config, rdb *goredis.Client) *Sender {
	if cfg.Subject == "" {
		cfg.Subject = "mailto:admin@localhost"
	}
	return &Sender{cfg: cfg, rdb: rdb, send: webpush.SendNotificationWithContext}
}

// Enabled reports whether VAPID keys are configured.
func (s *Sender) Enabled() bool { return s.cfg.Enabled() }

// PublicKey is the VAPID application server key the browser subscribes with.
func (s *Sender) PublicKey() string { return s.cfg.PublicKey }

// ParseSubscription validates a browser PushSubscription JSON body.
func ParseSubscription(body []byte) (*webpush.Subscription, error) {
	var sub webpush.Subscription
	if err := json.Unmarshal(body, &sub); err != nil {
		return nil, fmt.Errorf("invalid subscription JSON: %w", err)
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("subscription endpoint must be an https URL")
	}
	if sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return nil, errors.New("subscription keys missing")
	}
	return &sub, nil
}

// Subscribe stores sub, replacing any earlier one with the same endpoint.
func (s *Sender) Subscribe(ctx context.Context, sub *webpush.Subscription) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	b, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	return s.rdb.HSet(ctx, subsKey, sub.Endpoint, b).Err()
}

// Unsubscribe removes the subscription with this endpoint.
func (s *Sender) Unsubscribe(ctx context.Context, endpoint string) error {
	return s.rdb.HDel(ctx, subsKey, endpoint).Err()
}

// Send delivers n to every stored subscription and returns how many
// deliveries the push services accepted. Subscriptions the push service
// reports as gone (404/410) are deleted.
func (s *Sender) Send(ctx context.Context, n Notification) (int, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}
	all, err := s.rdb.HGetAll(ctx, subsKey).Result()
	if err != nil {
		return 0, err
	}
	return s.deliver(ctx, all, n)
}

// SendTo delivers n only to the stored subscription with this endpoint, so
// a device can test its own alerts without pinging every other device.
func (s *Sender) SendTo(ctx context.Context, endpoint string, n Notification) (int, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}
	raw, err := s.rdb.HGet(ctx, subsKey, endpoint).Result()
	if errors.Is(err, goredis.Nil) {
		return 0, ErrNotSubscribed
	}
	if err != nil {
		return 0, err
	}
	return s.deliver(ctx, map[string]string{endpoint: raw}, n)
}

// deliver sends n to subs (endpoint -> subscription JSON).
func (s *Sender) deliver(ctx context.Context, subs map[string]string, n Notification) (int, error) {
	msg, err := json.Marshal(n)
	if err != nil {
		return 0, err
	}
	opts := &webpush.Options{
		Subscriber:      s.cfg.Subject,
		VAPIDPublicKey:  s.cfg.PublicKey,
		VAPIDPrivateKey: s.cfg.PrivateKey,
		TTL:             pushTTL,
		Urgency:         webpush.UrgencyHigh,
	}

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ok  int
		sem = make(chan struct{}, maxInFlight)
	)
	for endpoint, raw := range subs {
		var sub webpush.Subscription
		if err := json.Unmarshal([]byte(raw), &sub); err != nil {
			log.Printf("[push] dropping unparsable subscription %s: %v", endpoint, err)
			s.rdb.HDel(ctx, subsKey, endpoint)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(endpoint string, sub webpush.Subscription) {
			defer wg.Done()
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, sendTimeout)
			defer cancel()
			resp, err := s.send(sctx, msg, &sub, opts)
			if err != nil {
				log.Printf("[push] send to %s failed: %v", host(endpoint), err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				log.Printf("[push] subscription gone (%d), removing %s", resp.StatusCode, host(endpoint))
				s.rdb.HDel(context.Background(), subsKey, endpoint)
			case resp.StatusCode >= 300:
				log.Printf("[push] push service %s returned %d", host(endpoint), resp.StatusCode)
			default:
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(endpoint, sub)
	}
	wg.Wait()
	return ok, nil
}

// host keeps logs short and avoids printing the per-device endpoint path.
func host(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return "?"
}
