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
