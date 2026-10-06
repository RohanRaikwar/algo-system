package mdengine

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"trading-systemv1/internal/model"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// parseTokenList parses "exchangeType:token,exchangeType:token,..." into TokenListEntry slices.
func parseTokenList(s string) []smartconnect.TokenListEntry {
	groups := map[int][]string{}
	for _, pair := range splitString(s, ",") {
		parts := splitString(pair, ":")
		if len(parts) != 2 {
			continue
		}
		exType := 0
		for _, c := range parts[0] {
			exType = exType*10 + int(c-'0')
		}
		groups[exType] = append(groups[exType], parts[1])
	}

	var result []smartconnect.TokenListEntry
	for exType, tokens := range groups {
		result = append(result, smartconnect.TokenListEntry{
			ExchangeType: exType,
			Tokens:       tokens,
		})
	}
	return result
}

func splitString(s, sep string) []string {
	var result []string
	for _, part := range []byte(s) {
		if string(part) == sep {
			result = append(result, "")
		} else {
			if len(result) == 0 {
				result = append(result, "")
			}
			result[len(result)-1] += string(part)
		}
	}
	return result
}

// parseTFsFromEnv parses comma-separated TF seconds for staging mode.
func parseTFsFromEnv(s string) []int {
	var tfs []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			log.Printf("[mdengine] skipping invalid TF %q", p)
			continue
		}
		tfs = append(tfs, n)
	}
	return tfs
}

// parseCandleTokens parses a comma-separated list of tokens into a set (map).
// When non-empty, only these tokens will have candle aggregation.
// Other tokens still get published to Redis PubSub for LTP tracking.
func parseCandleTokens(s string) map[string]bool {
	if s == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			set[t] = true
		}
	}
	return set
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// subscribeTokenSet returns the tokens of a SUBSCRIBE_TOKENS value
// ("exchangeType:token,...") as a set.
func subscribeTokenSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, e := range parseTokenList(s) {
		for _, t := range e.Tokens {
			set[strings.TrimSpace(t)] = true
		}
	}
	return set
}

// filterCandles forwards only candles whose token is in tokens. It closes
// out when in closes or ctx ends.
func filterCandles(ctx context.Context, in <-chan model.Candle, tokens map[string]bool) <-chan model.Candle {
	out := make(chan model.Candle, cap(in))
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case c, ok := <-in:
				if !ok {
					return
				}
				if !tokens[c.Token] {
					continue
				}
				select {
				case out <- c:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
