package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/model"
)

// telegramView is /api/telegram: the tgsignal service's health and the
// latest messages it read, newest first, each with what stratengine did.
type telegramView struct {
	Running  bool                  `json:"running"` // status key present (tgsignal alive)
	Status   *model.TGStatus       `json:"status"`
	Messages []model.TGMessageView `json:"messages"`
}

func makeTelegramHandler(rdb *goredis.Client, ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		v := loadTelegramView(rctx, rdb)
		_ = json.NewEncoder(w).Encode(v)
	}
}

func loadTelegramView(ctx context.Context, rdb *goredis.Client) telegramView {
	v := telegramView{Messages: []model.TGMessageView{}}
	if raw, err := rdb.Get(ctx, model.TGStatusKey).Result(); err == nil {
		var st model.TGStatus
		if json.Unmarshal([]byte(raw), &st) == nil {
			v.Running, v.Status = true, &st
		}
	}
	raws, _ := rdb.LRange(ctx, model.TGMessagesKey, 0, -1).Result()
	outcomes, _ := rdb.HGetAll(ctx, model.TGOutcomesKey).Result()
	timelines, _ := rdb.HGetAll(ctx, model.TGTimelineKey).Result()
	for _, raw := range raws {
		var m model.TGMessageView
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		m.Outcome = outcomes[m.Key()]
		if raw := timelines[m.Key()]; raw != "" {
			_ = json.Unmarshal([]byte(raw), &m.Timeline)
		}
		v.Messages = append(v.Messages, m)
	}
	return v
}
