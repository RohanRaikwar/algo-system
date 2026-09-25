package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

func main() {
	totpCode, err := totp.GenerateCode(os.Getenv("ANGEL_TOTP_SECRET"), time.Now())
	if err != nil {
		log.Fatalf("TOTP generation failed: %v", err)
	}

	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: os.Getenv("ANGEL_API_KEY")})
	_, err = sc.GenerateSession(os.Getenv("ANGEL_CLIENT_CODE"), os.Getenv("ANGEL_PASSWORD"), totpCode)
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}
	log.Println("✅ logged in")

	for _, sym := range []string{"NIFTY30MAR2623000CE", "NIFTY30MAR2623000PE"} {
		res, err := sc.SearchScrip("NFO", sym)
		if err != nil {
			fmt.Printf("%s: ERROR %v\n", sym, err)
			continue
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		fmt.Printf("%s:\n%s\n\n", sym, string(data))
	}
}
