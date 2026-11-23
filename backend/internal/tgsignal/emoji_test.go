package tgsignal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

func TestEmojiHint(t *testing.T) {
	for text, want := range map[string]string{
		"✅✅":           "(emojis: ✅ = done/hit)",
		"310🔥🔥":        "(emojis: 🔥 = price rising)",
		"Exit ❌":       "(emojis: ❌ = stop/cancel)",
		"good morning": "",
		"🍀🍀":           "(emojis: 🍀 = four leaf clover)",
		"👍🏻":           "(emojis: 👍 = thumbs up sign)",
		"⚠️ Exit":      "(emojis: ⚠ = alert)",
	} {
		if got := emojiHint(text); got != want {
			t.Errorf("emojiHint(%q) = %q, want %q", text, got, want)
		}
	}
	if hasActionEmoji("🔥🔥") || hasActionEmoji("310➡️📈") || !hasActionEmoji("✔️") || !hasActionEmoji("👍") {
		t.Error("action emoji set wrong")
	}
}

// An emoji-only reply on a held call is read by the LLM with the trade
// context; its reading acts. A 🔥-only reply and an unreplied "✅" are
// not read.
func TestEmojiOnlyReplyReadInContext(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.ctxs.outcome("42:1", TGEventEntered, tNow)
	n := r.calls

	h.onMessage(context.Background(), reply(2, 1, "🔥🔥"), tNow)
	h.onMessage(context.Background(), msg(3, "✅"), tNow)
	if r.calls != n {
		t.Fatalf("decoration or unreplied emoji read by the LLM (%d reads)", r.calls-n)
	}

	r.res = LLMResult{Kind: "follow_up", Action: "target_hit", Confidence: "high", AboutCall: true, Reason: "✅ after target"}
	h.onMessage(context.Background(), reply(4, 1, "✅✅"), tNow)
	if r.calls != n+1 || !strings.HasPrefix(r.reply, ctxHeader) {
		t.Fatalf("emoji reply not read with context: reads %d, ctx %q", r.calls-n, r.reply)
	}
	if len(s.follows) != 1 || s.follows[0].Action != model.TGTargetHit {
		t.Fatalf("follow-ups %+v", s.follows)
	}
}

// The hint line reaches the model after the message.
func TestEmojiHintSentToModel(t *testing.T) {
	var user string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		user = req["messages"].([]any)[1].(map[string]any)["content"].(string)
		content, _ := json.Marshal(`{"kind":"noise","action":"none","new_sl":"","about_call":true,"confidence":"high","reason":"x"}`)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + string(content) + `},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	r := NewFastContextReader(srv.URL, "k", "m", "")
	if _, err := r.Read(context.Background(), "✅", ctxHeader+"\nCall ..."); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(user, "New message:\n✅\n(emojis: ✅ = done/hit)") {
		t.Fatalf("user message %q", user)
	}
}

// Every emoji in the groups' real history gets a meaning (glossary or
// Unicode name), none falls back to a bare "emoji".
func TestEmojiHistoryAllNamed(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.jsonl")
	if len(files) == 0 {
		t.Skip("no history")
	}
	seen := map[rune]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			var hm HistoryMsg
			if json.Unmarshal([]byte(line), &hm) != nil {
				continue
			}
			for _, r := range emojis(hm.Text) {
				seen[r] = true
			}
		}
	}
	for r := range seen {
		if emojiMeaningOf(r) == "emoji" {
			t.Errorf("%q (%U) has no meaning", r, r)
		}
	}
	t.Logf("%d distinct emojis in history, all named", len(seen))
}
