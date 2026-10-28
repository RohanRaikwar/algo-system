package tgsignal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each read sends only the prompt parts it can need.
func TestSystemPromptFor(t *testing.T) {
	if got := systemPromptFor(realCall, ""); got != llmCallPrompt {
		t.Fatal("call check does not use the call prompt")
	}
	if got := systemPromptFor("Sl ctc kar lena", ctxHeader+"\nCall ..."); got != llmContextPrompt {
		t.Fatal("context read does not use the context prompt")
	}
	if got := systemPromptFor("Nifty 22800 call le lo 150 ke upar", ""); got != llmSystemPrompt {
		t.Fatal("unknown message does not use the whole prompt")
	}
	if got := systemPromptFor("Sl ctc kar lena", realCall); got != llmSystemPrompt {
		t.Fatal("reply to a call does not use the whole prompt")
	}
	for _, p := range []string{llmCallPrompt, llmContextPrompt} {
		if len(p) >= len(llmSystemPrompt) {
			t.Fatalf("prompt not smaller: %d >= %d", len(p), len(llmSystemPrompt))
		}
	}
	for _, want := range []string{"activate:", "Abi ctc h", "about_call: true if"} {
		if !strings.Contains(llmContextPrompt, want) {
			t.Fatalf("context prompt lacks %q", want)
		}
	}
	if !strings.Contains(llmCallPrompt, "Stoploss : PAID") || strings.Contains(llmCallPrompt, "jaldi niklo") {
		t.Fatal("call prompt parts")
	}
}

// A reply cut off at the small cap is asked again with room.
func TestReadRetriesTruncatedReply(t *testing.T) {
	var caps []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		caps = append(caps, req.MaxTokens)
		content, finish := `{"english":"Buy Nifty`, "length"
		if req.MaxTokens > llmMaxTokens {
			b, _ := json.Marshal(LLMResult{Kind: "noise", Action: "none", Confidence: "high"})
			content, finish = string(b), "stop"
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": finish}}})
		w.Write(b)
	}))
	defer srv.Close()
	res, err := NewOpenAIReader(srv.URL, "k", "m", "").Read(context.Background(), "hello", "")
	if err != nil || res.Kind != "noise" {
		t.Fatalf("read %+v %v", res, err)
	}
	if len(caps) != 2 || caps[0] != llmMaxTokens || caps[1] != llmMaxTokensReasoning {
		t.Fatalf("max_tokens sent %v", caps)
	}
}
