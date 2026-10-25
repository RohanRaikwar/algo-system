//go:build llmeval

// Manual check of the LLM prompt against this group's real message formats.
// It calls the live API (costs tokens), so it is behind a build tag:
//
//	set -a; source ../.env.prod; set +a
//	go test -tags llmeval ./internal/tgsignal/ -run TestLLMEval -v
//
// Run it after any change to llmSystemPrompt. Every call must come out
// SENT [AGREE]; "Sl ctc kar lena" FOLLOW_UP move_sl; the rest NOISE/SKIPPED.
package tgsignal

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestLLMEval(t *testing.T) {
	r := NewOpenAIReader(os.Getenv("TG_LLM_BASE_URL"), os.Getenv("TG_LLM_API_KEY"), os.Getenv("TG_LLM_MODEL"), os.Getenv("TG_LLM_REASONING"))
	disc := "\n\n\nWait for entry\n\nDisclaimer: Investments in securities markets are subject to market risks. All trade ideas and information shared are for educational and informational purposes only and do not constitute investment advice"
	call := "F&O | ⚡ INTRADAY\n\n🟢 BUY NIFTY PE 22800\n\n💰 Entry: Near Above 172.00\n🎯 Target:1st: ₹184.00 ,2nd:194\n🛑 Stop Loss: ₹166.00" + disc
	for _, c := range []struct{ text, reply string }{
		{"https://www.youtube.com/live/kGTwad3ycYw?si=ofQYc8xIAQgdGbkE\nAjao bhai aaj mast maje ane bale hai", ""},
		{call, ""},
		{"F&O | ⚡ INTRADAY\n\n🟢 BUY NIFTY PE 22750\n\n💰 Entry: Near Above 191.00\n🎯 Target:1st: ₹202.00 , 2ND:221\n🛑 Stop Loss: ₹182.00\n\n\nWait for entry\n\nCandle closing ka wait karna" + disc, ""},
		{"F&O | ⚡ INTRADAY\n\n🟢 BUY NIFTY CE 22700\n\n💰 Entry: Near Above 152.00\n🎯 Target:1st: ₹163.00 ,2nd:193\n🛑 Stop Loss: ₹146.00" + disc, ""},
		{"Wait for entry", call},
		{"Sl ctc kar lena", call},
		{"Kuchh profit book kar lena", call},
		{"jaldi niklo bhai", call},
		{"nikal jao abhi", call},
		{"bahar aa jao sab", call},
		{"Abi active nhi hua h wait karna", call},
		{"First target done ✔️", call},
	} {
		fmt.Printf("MSG: %.50q\n  => %s\n", c.text, Explain(context.Background(), r, c.text, c.reply, c.reply != ""))
		time.Sleep(3 * time.Second)
	}
}
