package mdengine

import (
	"testing"

	"trading-systemv1/pkg/smartconnect"
)

func TestParseSubscribeCommandMode(t *testing.T) {
	sub, ok := parseSubscribeCommand(`{"exchange_type":2,"tokens":["40712"],"mode":3}`)
	if !ok || sub.Mode != 3 || sub.Tokens[0].ExchangeType != 2 || sub.Tokens[0].Tokens[0] != "40712" {
		t.Fatalf("sub = %+v ok=%v", sub, ok)
	}
	old, ok := parseSubscribeCommand(`{"exchange_type":2,"tokens":["40712"]}`)
	if !ok || old.Mode != 2 {
		t.Fatalf("command without mode must default to Quote (2): %+v", old)
	}
	if _, ok := parseSubscribeCommand(`{"exchange_type":2,"tokens":[]}`); ok {
		t.Fatal("empty token list accepted")
	}
}

func TestRememberDynamicTokensKeepsMode(t *testing.T) {
	s := &Service{cfg: Config{TokenList: []smartconnect.TokenListEntry{{ExchangeType: 1, Tokens: []string{"99926000"}}}}}
	s.rememberDynamicTokens(dynSub{Mode: 3, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"40712"}}}})
	s.rememberDynamicTokens(dynSub{Mode: 2, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"57710"}}}})
	extra := s.sessionExtraSubs()
	if len(extra[3]) != 1 || extra[3][0].Tokens[0] != "40712" || len(extra[2]) != 1 || extra[2][0].Tokens[0] != "57710" {
		t.Fatalf("extra = %+v", extra)
	}
	if base := s.sessionTokenList(); len(base) != 1 || base[0].Tokens[0] != "99926000" {
		t.Fatalf("base list must be the configured tokens only: %+v", base)
	}
}

// A dynamic subscribe command may repeat the same (mode, exchangeType,
// token) many times over a session (e.g. the strike ladder re-touching a
// contract it already subscribed). rememberDynamicTokens must dedupe per
// (mode, exchangeType) rather than append a fresh TokenListEntry each call,
// or the remembered list — replayed on every reconnect — grows without
// bound.
func TestRememberDynamicTokensDedupesPerModeAndExchange(t *testing.T) {
	s := &Service{cfg: Config{TokenList: []smartconnect.TokenListEntry{{ExchangeType: 1, Tokens: []string{"99926000"}}}}}
	s.rememberDynamicTokens(dynSub{Mode: 3, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"40712"}}}})
	s.rememberDynamicTokens(dynSub{Mode: 3, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"40712"}}}}) // repeat
	s.rememberDynamicTokens(dynSub{Mode: 3, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"57710"}}}}) // new token, same mode+exchange

	extra := s.sessionExtraSubs()[3]
	if len(extra) != 1 {
		t.Fatalf("expected one TokenListEntry per (mode, exchangeType), got %d: %+v", len(extra), extra)
	}
	if got := extra[0].Tokens; len(got) != 2 || got[0] != "40712" || got[1] != "57710" {
		t.Fatalf("tokens = %v, want deduped [40712 57710]", got)
	}
}
