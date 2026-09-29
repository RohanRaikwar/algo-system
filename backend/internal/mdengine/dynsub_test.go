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
