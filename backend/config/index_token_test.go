package config

import "testing"

func TestIndexInstrument(t *testing.T) {
	for _, tc := range []struct{ env, sub, key, token string }{
		{"", "1:99926000", "NSE:99926000", "99926000"},
		{"99926009", "1:99926009", "NSE:99926009", "99926009"},
		{"NSE:99926000", "1:99926000", "NSE:99926000", "99926000"},
		{"bse:99919000", "3:99919000", "BSE:99919000", "99919000"},
		{"XYZ:123", "1:123", "NSE:123", "123"},
	} {
		t.Setenv("INDEX_TOKEN", tc.env)
		if IndexSubscribeTokens() != tc.sub || IndexKey() != tc.key || IndexToken() != tc.token {
			t.Errorf("INDEX_TOKEN=%q: got %q %q %q, want %q %q %q", tc.env,
				IndexSubscribeTokens(), IndexKey(), IndexToken(), tc.sub, tc.key, tc.token)
		}
	}
}

func TestIndexListsIncludeUnderlyings(t *testing.T) {
	t.Setenv("INDEX_TOKEN", "")
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	if got := IndexSubscribeTokens(); got != "1:99926000,3:99919000" {
		t.Errorf("IndexSubscribeTokens = %q", got)
	}
	if got := IndexKeys(); got != "NSE:99926000,BSE:99919000" {
		t.Errorf("IndexKeys = %q", got)
	}
	if IndexKey() != "NSE:99926000" || IndexToken() != "99926000" {
		t.Errorf("primary index changed")
	}
	t.Setenv("UNDERLYINGS", "")
	if got := IndexSubscribeTokens(); got != "1:99926000" {
		t.Errorf("default IndexSubscribeTokens = %q", got)
	}
}

// The old override variables are ignored: the index lists come only from
// INDEX_TOKEN and UNDERLYINGS.
func TestOldOverridesIgnored(t *testing.T) {
	t.Setenv("INDEX_TOKEN", "")
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	t.Setenv("SUBSCRIBE_TOKENS", "1:99926000")
	t.Setenv("STRAT_SUBSCRIBE_TOKENS", "NSE:99926000")
	if got := IndexSubscribeTokens(); got != "1:99926000,3:99919000" {
		t.Errorf("IndexSubscribeTokens = %q", got)
	}
	if got := IndexKeys(); got != "NSE:99926000,BSE:99919000" {
		t.Errorf("IndexKeys = %q", got)
	}
}

// EXITWATCH_INDEX_KEYS still overrides, with the extra underlyings added.
func TestIndexKeysEnv(t *testing.T) {
	t.Setenv("INDEX_TOKEN", "")
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	t.Setenv("EXITWATCH_INDEX_KEYS", "NSE:99926000")
	if got := IndexKeysEnv("EXITWATCH_INDEX_KEYS"); got != "NSE:99926000,BSE:99919000" {
		t.Errorf("IndexKeysEnv = %q", got)
	}
}

// INDEX_TOKEN as a list: first is the primary, the rest are extra indexes.
func TestIndexTokenList(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	t.Setenv("INDEX_TOKEN", "NSE:99926000,BSE:99919000")
	if IndexKey() != "NSE:99926000" || IndexToken() != "99926000" || IndexExchange() != "NSE" {
		t.Errorf("primary = %s", IndexKey())
	}
	if got := IndexSubscribeTokens(); got != "1:99926000,3:99919000" {
		t.Errorf("IndexSubscribeTokens = %q", got)
	}
	if got := IndexKeys(); got != "NSE:99926000,BSE:99919000" {
		t.Errorf("IndexKeys = %q", got)
	}
}
