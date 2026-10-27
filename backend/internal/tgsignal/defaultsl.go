package tgsignal

import (
	"errors"
	"fmt"

	"trading-systemv1/internal/model"
)

// defaultSL is the stop pct% below the trigger, for calls that give none.
func defaultSL(trigger, pct int64) int64 { return trigger * (100 - pct) / 100 }

// withDefaultSL gives a regex call without a stop (ErrNoSL) the default
// stop. pct 0 keeps the refusal.
func withDefaultSL(rx model.TGSignal, rxErr error, pct int64) (model.TGSignal, error) {
	if pct <= 0 || !errors.Is(rxErr, ErrNoSL) {
		return rx, rxErr
	}
	rx.SLPaise, rx.SLDefault = defaultSL(rx.TriggerPaise, pct), true
	return rx, rx.Validate()
}

// llmWithDefaultSL does the same for the LLM's reading of a new call, so
// both readers still have to agree on the rest.
func llmWithDefaultSL(r LLMResult, pct int64) LLMResult {
	if pct <= 0 || r.Kind != "new_call" || toPaise(cleanPrice(r.SL)) > 0 {
		return r
	}
	trig := toPaise(cleanPrice(r.Trigger))
	if trig <= 0 {
		return r
	}
	sl := defaultSL(trig, pct)
	r.SL, r.slDefault = fmt.Sprintf("%d.%02d", sl/100, sl%100), true
	return r
}
