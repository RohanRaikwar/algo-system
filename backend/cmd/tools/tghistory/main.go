// tghistory prints a chat's recent messages and how tgsignal reads each,
// read-only. Uses the tglogin session.
//
//	go run ./cmd/tools/tghistory -chat 4319557491 -n 50           # regex verdicts
//	go run ./cmd/tools/tghistory -chat 4319557491 -n 200 -replay  # regex + LLM, as the service decides
//	go run ./cmd/tools/tghistory -chat 4319557491 -n 500 -dump internal/tgsignal/testdata/indian_operator.jsonl
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"trading-systemv1/internal/tgsignal"
)

func main() {
	chat := flag.Int64("chat", 0, "chat id (from tglogin -list)")
	n := flag.Int("n", 50, "messages to read (max 100 per page, paged)")
	replay := flag.Bool("replay", false, "also read each message with the LLM (TG_LLM_* settings) and show the decision")
	dump := flag.String("dump", "", "write messages as JSON lines to this file (golden test set)")
	flag.Parse()

	cfg := tgsignal.LoadConfig()
	var reader tgsignal.CallReader
	if *replay {
		if cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
			log.Fatal("-replay needs TG_LLM_API_KEY and TG_LLM_MODEL")
		}
		reader = tgsignal.NewOpenAIReader(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMReason)
	}
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: cfg.SessionPath},
	})
	err := client.Run(context.Background(), func(ctx context.Context) error {
		api := client.API()
		peer, err := findPeer(ctx, api, *chat)
		if err != nil {
			return err
		}
		msgs, err := history(ctx, api, peer, *n)
		if err != nil {
			return err
		}
		var out *json.Encoder
		if *dump != "" {
			f, err := os.Create(*dump)
			if err != nil {
				return err
			}
			defer f.Close()
			out = json.NewEncoder(f)
		}
		texts := map[int]string{}
		for _, m := range msgs {
			texts[m.MsgID] = m.Text
		}
		for _, m := range msgs {
			if out != nil {
				if err := out.Encode(m); err != nil {
					return err
				}
				continue
			}
			reply := ""
			if m.ReplyTo != 0 {
				reply = fmt.Sprintf(" ↩ #%d", m.ReplyTo)
			}
			fmt.Printf("── #%d %s%s\n%s\n", m.MsgID, m.Sent.Local().Format("01-02 15:04"), reply, strings.TrimSpace(m.Text))
			fmt.Printf("   => %s\n", tgsignal.Explain(ctx, reader, m.Text, texts[m.ReplyTo], m.ReplyTo != 0, cfg.DefaultSLPct))
		}
		if out != nil {
			fmt.Printf("wrote %d messages to %s\n", len(msgs), *dump)
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}

// history reads up to n text messages, oldest first.
func history(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, n int) ([]tgsignal.HistoryMsg, error) {
	var out []tgsignal.HistoryMsg
	offset := 0
	for len(out) < n {
		limit := min(100, n-len(out))
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: limit, OffsetID: offset})
		if err != nil {
			return nil, err
		}
		var msgs []tg.MessageClass
		switch r := res.(type) {
		case *tg.MessagesMessages:
			msgs = r.Messages
		case *tg.MessagesMessagesSlice:
			msgs = r.Messages
		case *tg.MessagesChannelMessages:
			msgs = r.Messages
		}
		if len(msgs) == 0 {
			break
		}
		for _, mc := range msgs {
			m, ok := mc.(*tg.Message)
			if !ok {
				continue
			}
			offset = m.ID
			if m.Message == "" {
				continue
			}
			h := tgsignal.HistoryMsg{MsgID: m.ID, Text: m.Message, Sent: time.Unix(int64(m.Date), 0)}
			if r, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
				h.ReplyTo, _ = r.GetReplyToMsgID()
			}
			out = append(out, h)
		}
		time.Sleep(300 * time.Millisecond) // stay well under Telegram's flood limits
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func findPeer(ctx context.Context, api *tg.Client, id int64) (tg.InputPeerClass, error) {
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100})
	if err != nil {
		return nil, err
	}
	var chats []tg.ChatClass
	switch r := res.(type) {
	case *tg.MessagesDialogs:
		chats = r.Chats
	case *tg.MessagesDialogsSlice:
		chats = r.Chats
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Channel:
			if c.ID == id {
				return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}, nil
			}
		case *tg.Chat:
			if c.ID == id {
				return &tg.InputPeerChat{ChatID: c.ID}, nil
			}
		}
	}
	return nil, fmt.Errorf("chat %d not in your first 100 dialogs", id)
}
