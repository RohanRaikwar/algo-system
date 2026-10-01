// cmd/feedseqcheck — verify that parallel Angel feed sockets see the same
// sequence_number for the same packet, which ws.FeedGroup's dedup relies on.
//
// Opens two sockets (pinned to different Angel IPs, like mdengine), subscribes
// both to the same tokens in Quote mode, and every 10s reports per (token,
// seq) seen on both sockets whether the payload (exchange time, LTP, day
// volume) matched.
//
// Uses 2 of the 3 sockets Angel allows per client code: stop other local
// feeds first, and keep production at FEED_CONNECTIONS=1 while it runs.
//
// Usage:
//
//	set -a && source ../.env && set +a
//	go run ./cmd/feedseqcheck -tokens 1:99926000 -dur 60s
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"

	"trading-systemv1/pkg/smartconnect"
)

type pkt struct {
	exTS, ltp, vol int64
}

type key struct {
	token string
	seq   int64
}

func main() {
	tokensFlag := flag.String("tokens", "1:99926000", "exchangeType:token,... to subscribe")
	dur := flag.Duration("dur", 60*time.Second, "how long to compare")
	flag.Parse()

	apiKey := os.Getenv("ANGEL_API_KEY")
	clientCode := os.Getenv("ANGEL_CLIENT_CODE")
	totpCode, err := totp.GenerateCode(os.Getenv("ANGEL_TOTP_SECRET"), time.Now())
	if err != nil {
		log.Fatalf("TOTP: %v", err)
	}
	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: apiKey})
	session, err := sc.GenerateSession(clientCode, os.Getenv("ANGEL_PASSWORD"), totpCode)
	if err != nil {
		log.Fatalf("login: %v", err)
	}
	data, _ := session["data"].(map[string]interface{})
	jwt, _ := data["jwtToken"].(string)
	feed := sc.GetFeedToken()
	if jwt == "" || feed == "" {
		log.Fatal("login returned no tokens")
	}

	tokens := parseTokens(*tokensFlag)
	var mu sync.Mutex
	seen := [2]map[key]pkt{{}, {}}
	frames := [2]int{}
	// Same (token, seq) seen again on one socket: identical resend, or new data?
	repeatSame, repeatNew := [2]int{}, [2]int{}

	for i := 0; i < 2; i++ {
		ws, err := smartconnect.NewSmartWebSocketV3(jwt, apiKey, clientCode, feed, 3, 0, 5, 2, 5)
		if err != nil {
			log.Fatal(err)
		}
		ws.Dialer = smartconnect.NewPinnedDialer(i)
		ws.AddSubscription(smartconnect.ModeQuote, tokens)
		i := i
		ws.OnData = func(m map[string]interface{}) {
			k := key{fmt.Sprint(m["token"]), toI(m["sequence_number"])}
			p := pkt{toI(m["exchange_timestamp"]), toI(m["last_traded_price"]), toI(m["volume_trade_for_the_day"])}
			mu.Lock()
			frames[i]++
			if old, ok := seen[i][k]; ok && k.seq > 0 {
				if old == p {
					repeatSame[i]++
				} else {
					repeatNew[i]++
					if repeatNew[i] <= 3 {
						log.Printf("  REPEAT-NEW socket%d token=%s seq=%d: %+v -> %+v", i, k.token, k.seq, old, p)
					}
				}
			}
			seen[i][k] = p
			mu.Unlock()
		}
		if err := ws.Connect(); err != nil {
			log.Fatalf("socket %d connect: %v", i, err)
		}
		defer ws.CloseConnection()
		log.Printf("socket %d connected", i)
	}

	end := time.After(*dur)
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			report(&mu, &seen, &frames)
			log.Printf("  seq>0 repeats on one socket: identical s0=%d s1=%d | NEW DATA s0=%d s1=%d", repeatSame[0], repeatSame[1], repeatNew[0], repeatNew[1])
		case <-end:
			report(&mu, &seen, &frames)
			return
		}
	}
}

func report(mu *sync.Mutex, seen *[2]map[key]pkt, frames *[2]int) {
	mu.Lock()
	defer mu.Unlock()
	both, same, diff, only0, only1 := 0, 0, 0, 0, 0
	for k, p0 := range seen[0] {
		p1, ok := seen[1][k]
		if !ok {
			only0++
			continue
		}
		both++
		if p0 == p1 {
			same++
		} else {
			diff++
			if diff <= 5 {
				log.Printf("  MISMATCH token=%s seq=%d: socket0=%+v socket1=%+v", k.token, k.seq, p0, p1)
			}
		}
	}
	for k := range seen[1] {
		if _, ok := seen[0][k]; !ok {
			only1++
		}
	}
	log.Printf("frames s0=%d s1=%d | seqs on both=%d identical=%d mismatched=%d | only s0=%d only s1=%d",
		frames[0], frames[1], both, same, diff, only0, only1)
	if both > 0 && diff == 0 {
		log.Printf("  ✅ same seq ⇒ same packet on both sockets: seq dedup is safe")
	}
}

func parseTokens(s string) []smartconnect.TokenListEntry {
	byEx := map[int][]string{}
	for _, part := range strings.Split(s, ",") {
		var ex int
		var tok string
		if _, err := fmt.Sscanf(strings.Replace(part, ":", " ", 1), "%d %s", &ex, &tok); err != nil {
			log.Fatalf("bad token %q (want exchangeType:token)", part)
		}
		byEx[ex] = append(byEx[ex], tok)
	}
	var out []smartconnect.TokenListEntry
	for ex, toks := range byEx {
		out = append(out, smartconnect.TokenListEntry{ExchangeType: ex, Tokens: toks})
	}
	return out
}

func toI(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case uint64:
		return int64(t)
	case int32:
		return int64(t)
	}
	return 0
}
