package tgsignal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"trading-systemv1/internal/model"
)

// Service follows the configured Telegram chats as the logged-in user,
// publishes each buy call on model.TGSignalChannel and each follow-up on
// model.TGFollowUpChannel, and keeps the review log.
type Service struct {
	cfg     Config
	rdb     *redis.Client
	journal *Journal
	status  *statusTracker
	h       *handler
	runCtx  context.Context // Run's context, for message goroutines
}

// New builds the service. The session file must exist (tglogin).
func New(cfg Config) (*Service, error) {
	if seeded, err := cfg.SeedSession(); err != nil {
		return nil, err
	} else if seeded {
		log.Printf("[tgsignal] session written to %s from TG_SESSION_B64", cfg.SessionPath)
	}
	if _, err := os.Stat(cfg.SessionPath); err != nil {
		return nil, fmt.Errorf("no Telegram session at %s — run: go run ./cmd/tools/tglogin", cfg.SessionPath)
	}
	journal, err := OpenJournal(cfg.JournalPath)
	if err != nil {
		return nil, fmt.Errorf("journal %s: %w", cfg.JournalPath, err)
	}
	svc := &Service{cfg: cfg, rdb: redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword}), journal: journal}

	var reader CallReader
	if cfg.LLM {
		r := NewOpenAIReader(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMReason)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := r.CheckModel(ctx)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("LLM %s: %w", cfg.LLMBaseURL, err)
		}
		reader = r
		log.Printf("[tgsignal] 🧠 LLM reader on: %s at %s", cfg.LLMModel, cfg.LLMBaseURL)
	} else {
		log.Printf("[tgsignal] LLM reader off (TG_LLM=off): regex only, follow-ups ignored")
	}
	svc.status = newStatusTracker(cfg)
	svc.h = newHandler(cfg, reader, journal, svc.publishCall, svc.publishFollowUp)
	svc.h.status, svc.h.sink = svc.status, svc.showMessage
	return svc, nil
}

func (svc *Service) publishCall(sig model.TGSignal) error {
	return svc.publishJSON(model.TGSignalChannel, sig)
}

func (svc *Service) publishFollowUp(f model.TGFollowUp) error {
	return svc.publishJSON(model.TGFollowUpChannel, f)
}

func (svc *Service) publishJSON(channel string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return svc.rdb.Publish(ctx, channel, payload).Err()
}

// Run connects and follows the chats until ctx ends. Telegram reconnects
// inside the client; a returned error ends the process (air restarts it).
func (svc *Service) Run(ctx context.Context) error {
	defer svc.rdb.Close()
	defer svc.journal.Close()
	svc.runCtx = ctx
	go svc.outcomeLoop(ctx)
	go svc.statusLoop(ctx)
	defer svc.publishStatus() // final "disconnected" state

	d := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{Handler: d})
	d.OnNewMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewMessage) error {
		svc.onMessage(ctx, u.Message, false)
		return nil
	})
	d.OnNewChannelMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewChannelMessage) error {
		svc.onMessage(ctx, u.Message, false)
		return nil
	})
	d.OnEditMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateEditMessage) error {
		svc.onMessage(ctx, u.Message, true)
		return nil
	})
	d.OnEditChannelMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateEditChannelMessage) error {
		svc.onMessage(ctx, u.Message, true)
		return nil
	})

	client := telegram.NewClient(svc.cfg.APIID, svc.cfg.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: svc.cfg.SessionPath},
		UpdateHandler:  gaps,
	})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status: %w", err)
		}
		if !status.Authorized {
			return fmt.Errorf("session %s not logged in — run: go run ./cmd/tools/tglogin", svc.cfg.SessionPath)
		}
		log.Printf("[tgsignal] 🟢 logged in as %s, following chats %v", status.User.Username, svc.cfg.ChatIDs)
		name := status.User.Username
		if name == "" {
			name = strings.TrimSpace(status.User.FirstName + " " + status.User.LastName)
		}
		svc.status.telegramUp(name, time.Now())
		svc.publishStatus()
		err = gaps.Run(ctx, client.API(), status.User.ID, updates.AuthOptions{})
		svc.status.telegramDown(err)
		return err
	})
}

func (svc *Service) onMessage(_ context.Context, m tg.MessageClass, edited bool) {
	msg, ok := m.(*tg.Message)
	if !ok || msg.Out {
		return
	}
	in, ok := toInMsg(msg, edited)
	if !ok {
		return
	}
	// The LLM call can take seconds; never hold up the update loop. The
	// update's context ends with the handler, so use Run's.
	go svc.h.onMessage(svc.runCtx, in, time.Now())
}

func toInMsg(msg *tg.Message, edited bool) (inMsg, bool) {
	in := inMsg{MsgID: msg.ID, Text: msg.Message, Sent: time.Unix(int64(msg.Date), 0), Edited: edited}
	switch p := msg.PeerID.(type) {
	case *tg.PeerChannel:
		in.ChatID = p.ChannelID
	case *tg.PeerChat:
		in.ChatID = p.ChatID
	default:
		return in, false
	}
	if edited {
		in.Posted = in.Sent
		if ed, ok := msg.GetEditDate(); ok {
			in.Sent = time.Unix(int64(ed), 0)
		}
	}
	if r, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
		if id, ok := r.GetReplyToMsgID(); ok {
			in.ReplyTo = id
		}
	}
	return in, true
}

// outcomeLoop writes stratengine's outcomes into the review log.
func (svc *Service) outcomeLoop(ctx context.Context) {
	for ctx.Err() == nil {
		sub := svc.rdb.Subscribe(ctx, model.TGOutcomeChannel)
		for msg := range sub.Channel() {
			var o model.TGOutcome
			if err := json.Unmarshal([]byte(msg.Payload), &o); err != nil {
				continue
			}
			chat, id, ok := splitCallKey(o.CallKey)
			if !ok {
				continue
			}
			line := outcomeLine(o)
			if err := svc.journal.AddOutcome(chat, id, line); err != nil {
				log.Printf("[tgsignal] journal outcome: %v", err)
			}
			svc.addOutcome(ctx, o.CallKey, line)
			if o.MS > 0 {
				svc.addStage(ctx, o.CallKey, model.TGStage{Event: o.Event, MS: o.MS})
			}
		}
		_ = sub.Close()
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// outcomeLine is "15:04:05 EVENT +1234ms: detail" (no +ms when unknown).
func outcomeLine(o model.TGOutcome) string {
	line := o.TS.In(time.Local).Format("15:04:05") + " " + o.Event
	if o.MS > 0 {
		line += fmt.Sprintf(" +%dms", o.MS)
	}
	if o.Detail != "" {
		line += ": " + o.Detail
	}
	return line
}

func splitCallKey(k string) (int64, int, bool) {
	a, b, ok := strings.Cut(k, ":")
	if !ok {
		return 0, 0, false
	}
	chat, err1 := strconv.ParseInt(a, 10, 64)
	id, err2 := strconv.Atoi(b)
	return chat, id, err1 == nil && err2 == nil
}

// statusLoop refreshes the dashboard status key until ctx ends. The key
// expires, so a stopped service shows as not running.
func (svc *Service) statusLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		svc.publishStatus()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (svc *Service) publishStatus() {
	b, err := json.Marshal(svc.status.snapshot(time.Now()))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.rdb.Set(ctx, model.TGStatusKey, b, time.Minute).Err(); err != nil {
		log.Printf("[tgsignal] status: %v", err)
	}
}

// tgMessagesKept is how many messages the dashboard list keeps.
const tgMessagesKept = 100

// showMessage puts a message at the head of the dashboard list.
func (svc *Service) showMessage(v model.TGMessageView) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pipe := svc.rdb.TxPipeline()
	pipe.LPush(ctx, model.TGMessagesKey, b)
	pipe.LTrim(ctx, model.TGMessagesKey, 0, tgMessagesKept-1)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[tgsignal] dashboard list: %v", err)
	}
}

// addOutcome appends an outcome line to the call's entry in the outcomes
// hash (kept two days).
func (svc *Service) addOutcome(ctx context.Context, callKey, line string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	prev, _ := svc.rdb.HGet(ctx, model.TGOutcomesKey, callKey).Result()
	if prev != "" {
		line = prev + " | " + line
	}
	pipe := svc.rdb.TxPipeline()
	pipe.HSet(ctx, model.TGOutcomesKey, callKey, line)
	pipe.Expire(ctx, model.TGOutcomesKey, 48*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[tgsignal] outcome: %v", err)
	}
}

// addStage appends a step to the call's timeline (kept two days). Only
// outcomeLoop writes it, so the read-modify-write does not race.
func (svc *Service) addStage(ctx context.Context, callKey string, st model.TGStage) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var stages []model.TGStage
	if prev, err := svc.rdb.HGet(ctx, model.TGTimelineKey, callKey).Result(); err == nil {
		_ = json.Unmarshal([]byte(prev), &stages)
	}
	b, err := json.Marshal(append(stages, st))
	if err != nil {
		return
	}
	pipe := svc.rdb.TxPipeline()
	pipe.HSet(ctx, model.TGTimelineKey, callKey, b)
	pipe.Expire(ctx, model.TGTimelineKey, 48*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[tgsignal] timeline: %v", err)
	}
}
