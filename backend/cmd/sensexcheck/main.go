// Command sensexcheck verifies an extra underlying (SENSEX by default) can
// be traded before INDEX_TOKEN lists it. Read-only: it never orders.
//
// Without credentials it checks the instrument master only (lot size,
// strike step, listed expiries, nearest future, an ATM contract). With
// ANGEL_API_KEY / ANGEL_CLIENT_CODE / ANGEL_PASSWORD / ANGEL_TOTP_SECRET it
// also loads the OptionGreek chain the SR strike pick needs. Logging in
// opens a new Angel session; run it outside market hours if production
// shares the account.
//
//	go run ./cmd/sensexcheck            # SENSEX
//	go run ./cmd/sensexcheck -key NIFTY
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/underlying"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

func main() {
	key := flag.String("key", "SENSEX", "underlying profile key")
	spot := flag.Float64("spot", 0, "index level for the ATM check (rupees); 0 = mid of listed strikes")
	flag.Parse()
	p := underlying.Get(*key)
	fmt.Println("profile:", p)

	im := orderexec.GetInstrumentMaster()
	if err := im.EnsureLoaded(2 * time.Minute); err != nil {
		log.Fatalf("instrument master: %v", err)
	}
	now := time.Now()
	exps := im.OptionExpiriesOn(p.OptionName, p.FNOExchange, now)
	if len(exps) == 0 {
		log.Fatalf("❌ no %s options on %s in the instrument master", p.OptionName, p.FNOExchange)
	}
	show := exps
	if len(show) > 4 {
		show = show[:4]
	}
	fmt.Print("listed expiries:")
	for _, e := range show {
		fmt.Print(" ", e.Format("Mon 02Jan2006"))
	}
	fmt.Printf(" (%d total)\n", len(exps))
	step := im.StrikeStep(p.OptionName, p.FNOExchange, now)
	fmt.Printf("strike step: %d (profile %d)\n", step, p.StrikeStep)
	if fut, err := im.NearestFutureOn(p.OptionName, p.FNOExchange, now); err == nil {
		fmt.Printf("nearest future: %s token=%s lot=%s\n", fut.Symbol, fut.Token, fut.LotSize)
	} else {
		fmt.Println("⚠️  nearest future:", err)
	}

	sp := orderexec.NewStrikePickerFor(nil, p)
	atm := int64(*spot)
	if atm == 0 {
		atm = guessATM(im, p, exps[0])
	}
	if step > 0 {
		atm = (atm + step/2) / step * step
	}
	for _, opt := range []string{"CE", "PE"} {
		info, err := sp.ResolveStrikeOn(exps[0], atm, opt)
		if err != nil {
			fmt.Printf("❌ %d%s on %s: %v\n", atm, opt, exps[0].Format("02Jan2006"), err)
			continue
		}
		fmt.Printf("✅ %d%s → %s token=%s lot=%d\n", atm, opt, info.Symbol, info.Token, info.LotSize)
	}

	sc := login()
	if sc == nil {
		fmt.Println("no Angel credentials: OptionGreek not checked")
		return
	}
	chain, err := orderexec.NewStrikePickerFor(sc, p).LoadOptionChain(now)
	if err != nil || len(chain) == 0 {
		fmt.Printf("❌ OptionGreek %s: %d contracts, err=%v — SR entries will take the ATM strike without greek checks\n", p.OptionName, len(chain), err)
		return
	}
	fmt.Printf("✅ OptionGreek %s: %d contracts (e.g. %d%s delta=%.2f iv=%.1f)\n",
		p.OptionName, len(chain), chain[0].Strike, chain[0].OptionType, chain[0].Delta, chain[0].IV)
}

// guessATM picks a listed strike near the middle of the nearest expiry.
func guessATM(im *orderexec.InstrumentMaster, p underlying.Profile, exp time.Time) int64 {
	var strikes []int64
	for k := int64(1000); k < 200000; k += 100 {
		if im.FindOption(p.OptionName, p.FNOExchange, exp, k, "CE") != nil {
			strikes = append(strikes, k)
		}
	}
	if len(strikes) == 0 {
		return 0
	}
	return strikes[len(strikes)/2]
}

func login() *smartconnect.SmartConnect {
	apiKey, client := os.Getenv("ANGEL_API_KEY"), os.Getenv("ANGEL_CLIENT_CODE")
	pass, secret := os.Getenv("ANGEL_PASSWORD"), os.Getenv("ANGEL_TOTP_SECRET")
	if apiKey == "" || client == "" || pass == "" || secret == "" {
		return nil
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		log.Fatalf("TOTP: %v", err)
	}
	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: apiKey})
	if _, err := sc.GenerateSession(client, pass, code); err != nil {
		log.Fatalf("login: %v", err)
	}
	return sc
}
