package tgsignal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

// newFastHandler is a test handler whose trade-context messages go to
// fast, the rest to main.
func newFastHandler(t *testing.T, main, fast CallReader) (*handler, *sink, *Journal) {
	h, s, j := newTestHandler(t, main)
	h.setContextReader("fast", fast, llmLimits{})
	return h, s, j
}

func TestFastContextReadActs(t *testing.T) {
	main := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	fast := &fakeReader{res: LLMResult{Kind: "follow_up", Action: "activate", Confidence: "high", AboutCall: true, Reason: "live", Fast: true}}
	h, s, _ := newFastHandler(t, main, fast)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	n := main.calls
	h.onMessage(context.Background(), reply(2, 1, "Entry le lo abhi, market strong hai"), tNow)
	if fast.calls != 1 || !strings.HasPrefix(fast.reply, ctxHeader) {
		t.Fatalf("fast reads %d, context %q", fast.calls, fast.reply)
	}
	if main.calls != n {
		t.Fatalf("main model read a context message the fast model was sure of")
	}
	if got := activates(s); len(got) != 1 || got[0] != "42:2→42:1" {
		t.Fatalf("activates %v", got)
	}
}

func TestFastContextEscalates(t *testing.T) {
	for _, tc := range []struct {
		name string
		fast LLMResult
		err  error
	}{
		{"low confidence", LLMResult{Kind: "noise", Action: "none", Confidence: "low", AboutCall: true, Fast: true}, nil},
		{"new call", LLMResult{Kind: "new_call", Action: "none", Confidence: "high", Fast: true}, nil},
		{"error", LLMResult{}, errors.New("HTTP 400: bad")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
			fast := &fakeReader{res: tc.fast, err: tc.err}
			h, s, _ := newFastHandler(t, main, fast)
			h.onMessage(context.Background(), msg(1, realCall), tNow)
			n := main.calls
			main.res = LLMResult{Kind: "follow_up", Action: "exit", Confidence: "high", AboutCall: true, Reason: "get out"}
			h.onMessage(context.Background(), reply(2, 1, "bhai abhi bahar aa jao"), tNow)
			if fast.calls != 1 || main.calls != n+1 {
				t.Fatalf("fast %d, main %d (want 1, %d)", fast.calls, main.calls, n+1)
			}
			if !strings.HasPrefix(main.reply, ctxHeader) {
				t.Fatalf("main model lacks the context: %q", main.reply)
			}
			if len(s.follows) != 1 || s.follows[0].Action != model.TGExit || s.follows[0].CallKey() != "42:1" {
				t.Fatalf("follow-ups %+v", s.follows)
			}
		})
	}
}

// The fast reader sends the small prompt and schema for a context
// message, and the full ones for anything else.
func TestFastReaderRequest(t *testing.T) {
	var got map[string]any
	reply := `{"kind":"follow_up","action":"none","new_sl":"","about_call":true,"confidence":"high","reason":"wait"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		content, _ := json.Marshal(reply)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + string(content) + `},"finish_reason":"stop"}],"usage":{"total_tokens":300}}`))
	}))
	defer srv.Close()
	r := NewFastContextReader(srv.URL, "k", "fast-model", "")

	res, err := r.Read(context.Background(), "wait karna", ctxHeader+"\nCall 10:00 ...")
	if err != nil {
		t.Fatal(err)
	}
	rf := got["response_format"].(map[string]any)["json_schema"].(map[string]any)
	if rf["name"] != "telegram_context" || got["max_tokens"].(float64) != llmMaxTokensCtx {
		t.Fatalf("request %v / max_tokens %v", rf["name"], got["max_tokens"])
	}
	sys := got["messages"].([]any)[0].(map[string]any)["content"].(string)
	if sys != llmFastCtxPrompt {
		t.Fatalf("system prompt is not the fast one")
	}
	if !res.Fast || res.Kind != "noise" || res.Action != "none" || !res.AboutCall {
		t.Fatalf("normalized %+v", res)
	}

	reply = `{"english":"x","kind":"noise","underlying":"","strike":0,"option_type":"","trigger":"","sl":"","targets":[],"action":"none","new_sl":"","about_call":false,"confidence":"high","reason":"chat"}`
	res, err = r.Read(context.Background(), "BUY NIFTY 22750 PE above 191", "")
	if err != nil {
		t.Fatal(err)
	}
	if rf := got["response_format"].(map[string]any)["json_schema"].(map[string]any); rf["name"] != "telegram_call" || res.Fast {
		t.Fatalf("non-context message read with the fast schema: %v fast=%v", rf["name"], res.Fast)
	}
}

func TestContextEndpointDefaults(t *testing.T) {
	c := Config{LLM: true, LLMBaseURL: "https://a/v1", LLMAPIKey: "main", LLMModel: "big", LLMTPM: 8000, LLMRPM: 30}
	if c.ctxOn() {
		t.Fatal("on with no TG_LLM_CTX_MODEL")
	}
	c.LLMCtx = LLMEndpoint{Env: "TG_LLM_CTX", Model: "fast"}
	e := c.ctxEndpoint()
	if !c.ctxOn() || e.BaseURL != "https://a/v1" || e.APIKey != "main" || e.Model != "fast" || e.TPM != 8000 {
		t.Fatalf("endpoint %+v", e)
	}
	c.LLM = false
	if c.ctxOn() {
		t.Fatal("on with TG_LLM=off")
	}
}
