package tgsignal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"trading-systemv1/internal/model"
)

// LLMResult is the model's reading of one message. Prices are strings
// ("191.00") so no float reaches the money path; toPaise converts them.
type LLMResult struct {
	English    string   `json:"english"` // the message in plain English, written first
	Kind       string   `json:"kind"`    // new_call | follow_up | noise
	Underlying string   `json:"underlying"`
	Strike     int64    `json:"strike"`
	OptionType string   `json:"option_type"` // CE | PE | ""
	Trigger    string   `json:"trigger"`
	SL         string   `json:"sl"`
	Targets    []string `json:"targets"`
	Action     string   `json:"action"` // follow_up: exit | sl_hit | target_hit | move_sl | cancel | none
	NewSL      string   `json:"new_sl"`
	Confidence string   `json:"confidence"` // high | medium | low
	Reason     string   `json:"reason"`
	// Usage is the request's token count (set by the reader, not the model).
	Usage Usage `json:"-"`
	// slDefault: SL was missing and set by llmWithDefaultSL.
	slDefault bool
}

// Call converts a new_call reading into a TGSignal (without message ids).
func (r LLMResult) Call() model.TGSignal {
	s := model.TGSignal{
		Underlying:   strings.ToUpper(strings.TrimSpace(r.Underlying)),
		Strike:       r.Strike,
		OptionType:   strings.ToUpper(r.OptionType),
		TriggerPaise: toPaise(cleanPrice(r.Trigger)),
		SLPaise:      toPaise(cleanPrice(r.SL)),
		SLDefault:    r.slDefault,
	}
	for _, t := range r.Targets {
		if p := toPaise(cleanPrice(t)); p > 0 {
			s.Targets = append(s.Targets, p)
		}
	}
	return s
}

// FollowUp converts a follow_up reading (without message ids).
func (r LLMResult) FollowUp() model.TGFollowUp {
	return model.TGFollowUp{
		Action:     model.TGFollowUpAction(r.Action),
		NewSLPaise: toPaise(cleanPrice(r.NewSL)),
		Strike:     r.Strike,
		OptionType: strings.ToUpper(r.OptionType),
	}
}

var rePrice = regexp.MustCompile(`\d+(?:\.\d+)?`)

// cleanPrice keeps the first number of "₹191.00" or "191".
func cleanPrice(s string) string { return rePrice.FindString(s) }

// CallReader reads one message (and the message it replies to, if any).
type CallReader interface {
	Read(ctx context.Context, text, replyText string) (LLMResult, error)
}

// llmSystemPrompt teaches the model this group's style. Examples are real
// messages from the group.
const llmSystemPrompt = `Extract trade instructions from an Indian options Telegram group (Hinglish) as JSON.
First write "english": the message translated to plain English (short). Then decide every other field from that English meaning.

kind new_call: a NEW option buy, even if it also says "Wait for entry", "Candle closing ka wait karna" or has a disclaimer. "BUY NIFTY PE 22750" = strike 22750, PE (CALL=CE, PUT=PE). "Near Above 191"/"above 191"/"@191" -> trigger "191". "Stop Loss ₹182"/"SL 182" -> sl "182"; "SL 15 point" -> sl = trigger-15. targets: all target prices in order, skip 1st/2nd words. Prices are premiums, never the strike. "ENTRY PRICE ➖ 50" -> trigger "50" (a call even without the word BUY). "Stoploss : PAID"/"SL paid" (stop kept for paid members) -> sl "" and does not lower confidence; numbered "Target 1 60, Target 2 80" -> targets ["60","80"]. Two different contracts, levels for tomorrow, or a missing entry -> confidence "low".

kind follow_up: about an earlier call (reply text given).
- exit: "exit", "ctc exit", "close", "book full profit", "niklo", "jaldi niklo", "nikal jao", "bahar aa jao", "exit kar do". An exit needs no price: English "get out"/"exit"/"close" about the call = exit.
- sl_hit: "Sl hit", "Trailing sl hit"
- target_hit: "All target done"
- move_sl: "Sl ctc kar lena"/"SL cost" -> new_sl = replied call's entry; "sl 112 par le aao" -> new_sl "112"; no price -> new_sl ""
- cancel: "avoid", "cancel", "skip"

kind noise: messages with no call or follow-up: price ticks "193🔥"/"55 ✅", result recaps "280 To 400"/"70 ➜ 150 Target HIT", "Active", a lone "wait for entry", "First target done", "Abi ctc h", partial booking "Kuchh profit book kar lena", optional advice "Safe trader exit kar sakte h"/"Only risky traders", promotions, gold/forex.

Unused: "" strings, 0 strike, [] targets, action "none". confidence "high" only if every needed field is explicit. reason: few words.`

// llmSchema is the strict JSON schema of LLMResult.
var llmSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"english", "kind", "underlying", "strike", "option_type", "trigger", "sl", "targets", "action", "new_sl", "confidence", "reason"},
	"properties": map[string]any{
		"english":     map[string]any{"type": "string"},
		"kind":        map[string]any{"type": "string", "enum": []string{"new_call", "follow_up", "noise"}},
		"underlying":  map[string]any{"type": "string"},
		"strike":      map[string]any{"type": "integer"},
		"option_type": map[string]any{"type": "string", "enum": []string{"CE", "PE", ""}},
		"trigger":     map[string]any{"type": "string"},
		"sl":          map[string]any{"type": "string"},
		"targets":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"action":      map[string]any{"type": "string", "enum": []string{"exit", "sl_hit", "target_hit", "move_sl", "cancel", "none"}},
		"new_sl":      map[string]any{"type": "string"},
		"confidence":  map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}},
		"reason":      map[string]any{"type": "string"},
	},
}

// llmMaxTokens caps the reply; reasoning models need room for hidden
// reasoning at effort low.
const llmMaxTokens = 400

// OpenAIReader reads messages with any OpenAI-compatible chat API (Groq,
// xAI, OpenAI) using a strict JSON schema response.
type OpenAIReader struct {
	baseURL, apiKey, model string
	// reasoning is "reasoning_effort" for reasoning models (gpt-oss:
	// low|medium|high); "" sends none. Low keeps hidden reasoning tokens,
	// and so rate-limit use, small.
	reasoning string
	client    *http.Client
}

// Usage is one request's token count.
type Usage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

// NewOpenAIReader builds a reader for baseURL (e.g.
// https://api.groq.com/openai/v1).
func NewOpenAIReader(baseURL, apiKey, model, reasoning string) *OpenAIReader {
	return &OpenAIReader{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		model:     model,
		reasoning: reasoning,
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

// CheckModel confirms the configured model is served by the API.
func (r *OpenAIReader) CheckModel(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("models: HTTP %d: %s", resp.StatusCode, b)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return err
	}
	var ids []string
	for _, m := range list.Data {
		if m.ID == r.model {
			return nil
		}
		ids = append(ids, m.ID)
	}
	return fmt.Errorf("model %q not served; available: %s", r.model, strings.Join(ids, ", "))
}

// Read asks the model for one message's reading. On 429/5xx it waits the
// server's retry-after (capped) and tries again, up to 3 attempts, within
// ctx.
func (r *OpenAIReader) Read(ctx context.Context, text, replyText string) (LLMResult, error) {
	user := "Message:\n" + text
	if replyText != "" {
		user = "This message is a reply to:\n" + replyText + "\n\n" + user
	}
	req := map[string]any{
		"model":       r.model,
		"temperature": 0,
		// The JSON reply is ~60 tokens. A cap keeps the request inside
		// per-minute output-token limits, which count max_tokens up front.
		"max_tokens": llmMaxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": llmSystemPrompt},
			{"role": "user", "content": user},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "telegram_call", "strict": true, "schema": llmSchema},
		},
	}
	if r.reasoning != "" {
		req["reasoning_effort"] = r.reasoning
	}
	body, err := json.Marshal(req)
	if err != nil {
		return LLMResult{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		res, wait, err := r.post(ctx, body)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if wait == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return LLMResult{}, fmt.Errorf("%w (last: %v)", ctx.Err(), lastErr)
		case <-time.After(wait):
		}
	}
	return LLMResult{}, lastErr
}

// post sends one request. wait > 0 means retry after wait.
func (r *OpenAIReader) post(ctx context.Context, body []byte) (LLMResult, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return LLMResult{}, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return LLMResult{}, 2 * time.Second, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		var wait time.Duration
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			wait = retryAfter(resp.Header.Get("Retry-After"))
		}
		return LLMResult{}, wait, fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return LLMResult{}, 0, err
	}
	if len(out.Choices) == 0 {
		return LLMResult{}, 0, fmt.Errorf("no choices")
	}
	var res LLMResult
	if err := json.Unmarshal([]byte(out.Choices[0].Message.Content), &res); err != nil {
		return LLMResult{}, 0, fmt.Errorf("bad JSON from model (%s): %w", out.Choices[0].FinishReason, err)
	}
	res.Usage = out.Usage
	return res, 0, nil
}

// retryAfter parses a Retry-After seconds value: 1s to 20s, 3s if absent.
func retryAfter(v string) time.Duration {
	secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || secs <= 0 {
		return 3 * time.Second
	}
	d := time.Duration(secs*1000) * time.Millisecond
	return min(max(d, time.Second), 20*time.Second)
}

// llmKeywords mark a message worth an LLM call.
// Bare "pe"/"ce" are left out: "pe" is Hindi for "on" ("youtube pe").
var llmKeywords = regexp.MustCompile(`(?i)\b(buy|sell|nifty|sensex|target|tgt|sl|stop|stoploss|exit|book|cost|ctc|trail|trailing|cancel|avoid|hit|done|close|square)\b|\b\d{4,6}\s*(ce|pe)\b|\b(ce|pe)\s*\d{4,6}\b`)

// needsLLM skips messages that cannot be an instruction: price ticks like
// "193🔥🔥" and chat with no trade words ("Ajao bhai log"). A reply to a
// call always goes, whatever its words ("jaldi niklo" = exit now). It keeps
// LLM calls within free-tier rate limits.
func needsLLM(text string, replyToCall bool) bool {
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 3 {
		return false
	}
	return replyToCall || llmKeywords.MatchString(text)
}

// hostOf is the host of an API base URL, for display.
func hostOf(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return baseURL
}
