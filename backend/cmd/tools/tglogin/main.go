// tglogin logs the Telegram user in once and saves the session for the
// tgsignal service. With -list it prints the groups and channels the user
// is in, with the ids to put in TG_CHAT_IDS.
//
//	go run ./cmd/tools/tglogin          # phone from TG_PHONE, code from stdin
//	go run ./cmd/tools/tglogin -qr      # scan a QR in the Telegram app, no code
//	go run ./cmd/tools/tglogin -list
//	go run ./cmd/tools/tglogin -check -llm  # verify session (+ LLM key), never logs in
//	go run ./cmd/tools/tglogin -export      # session as base64 for TG_SESSION_B64 (production)
//
// Production gets its own session (TG_SESSION_PATH=data/tg-prod.session):
// one session used from two places at once can be cancelled by Telegram.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"rsc.io/qr"

	"trading-systemv1/internal/tgsignal"
)

func main() {
	list := flag.Bool("list", false, "print groups/channels and their ids")
	useQR := flag.Bool("qr", false, "log in by scanning a QR code (Settings → Devices → Link Desktop Device)")
	qrPNG := flag.String("qr-png", "data/tg-login-qr.png", "also write the QR code to this PNG")
	check := flag.Bool("check", false, "verify the saved session (and chats) without logging in")
	checkLLM := flag.Bool("llm", false, "with -check: also verify TG_LLM_* (API key and model)")
	export := flag.Bool("export", false, "print the session file as base64 (for TG_SESSION_B64)")
	flag.Parse()

	cfg := tgsignal.LoadConfig()
	if *export {
		raw, err := os.ReadFile(cfg.SessionPath)
		if err != nil {
			log.Fatalf("no session at %s: %v", cfg.SessionPath, err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(raw))
		return
	}
	if cfg.APIID == 0 || cfg.APIHash == "" {
		log.Fatal("TG_API_ID and TG_API_HASH are required (my.telegram.org → API development tools)")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SessionPath), 0o700); err != nil {
		log.Fatal(err)
	}
	in := bufio.NewReader(os.Stdin)
	ask := func(prompt string) string {
		fmt.Print(prompt)
		s, _ := in.ReadString('\n')
		return strings.TrimSpace(s)
	}
	phone := cfg.Phone
	if phone == "" && !*useQR {
		phone = ask("Phone (+91...): ")
	}

	d := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(d)
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: cfg.SessionPath},
		UpdateHandler:  d,
	})
	if *check {
		if err := runCheck(cfg, client, *checkLLM); err != nil {
			log.Fatalf("CHECK FAILED: %v", err)
		}
		return
	}
	err := client.Run(context.Background(), func(ctx context.Context) error {
		if *useQR {
			if err := loginQR(ctx, client, loggedIn, *qrPNG); err != nil {
				return err
			}
		} else {
			code := auth.CodeAuthenticatorFunc(func(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
				return ask("Code from Telegram: "), nil
			})
			flow := auth.NewFlow(auth.Constant(phone, os.Getenv("TG_PASSWORD"), code), auth.SendCodeOptions{})
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return fmt.Errorf("login (2FA accounts set TG_PASSWORD): %w", err)
			}
		}
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Logged in as %s %s (@%s). Session saved to %s\n", self.FirstName, self.LastName, self.Username, cfg.SessionPath)
		if *list {
			return printChats(ctx, client.API())
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}

func printChats(ctx context.Context, api *tg.Client) error {
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100})
	if err != nil {
		return err
	}
	var chats []tg.ChatClass
	switch r := res.(type) {
	case *tg.MessagesDialogs:
		chats = r.Chats
	case *tg.MessagesDialogsSlice:
		chats = r.Chats
	}
	fmt.Println("\nTG_CHAT_IDS candidates:")
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Channel:
			fmt.Printf("  %-14d %s\n", c.ID, c.Title)
		case *tg.Chat:
			fmt.Printf("  %-14d %s\n", c.ID, c.Title)
		}
	}
	return nil
}

// loginQR logs in by QR: the user scans it in the Telegram app (Settings →
// Devices → Link Desktop Device). No code is sent or typed.
func loginQR(ctx context.Context, client *telegram.Client, loggedIn qrlogin.LoggedIn, pngPath string) error {
	if status, err := client.Auth().Status(ctx); err == nil && status.Authorized {
		return nil
	}
	show := func(ctx context.Context, token qrlogin.Token) error {
		code, err := qr.Encode(token.URL(), qr.L)
		if err != nil {
			return err
		}
		printQR(code)
		if f, err := os.Create(pngPath); err == nil {
			_ = png.Encode(f, code.Image())
			f.Close()
			fmt.Printf("QR also saved to %s\n", pngPath)
		}
		fmt.Printf("Scan in Telegram app: Settings → Devices → Link Desktop Device (expires %s)\n", token.Expires().Format("15:04:05"))
		return nil
	}
	_, err := client.QR().Auth(ctx, loggedIn, show)
	if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		pwd := os.Getenv("TG_PASSWORD")
		if pwd == "" {
			return fmt.Errorf("account has 2FA: set TG_PASSWORD and run -qr again")
		}
		_, err = client.Auth().Password(ctx, pwd)
	}
	if err != nil {
		return fmt.Errorf("qr login: %w", err)
	}
	_ = os.Remove(pngPath)
	return nil
}

// printQR draws the code with half blocks, two rows per line.
func printQR(c *qr.Code) {
	const quiet = 2
	black := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y)
	}
	n := c.Size + 2*quiet
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := 0; x < n; x++ {
			top, bot := black(x, y), black(x, y+1)
			switch {
			case top && bot:
				b.WriteString(" ")
			case top:
				b.WriteString("▄")
			case bot:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		b.WriteString("\n")
	}
	fmt.Print(b.String())
}

// runCheck verifies the saved session can be used as is: authorized, and
// every TG_CHAT_IDS chat visible. It never prompts or logs in. With llm it
// also checks the LLM API key and model.
func runCheck(cfg tgsignal.Config, client *telegram.Client, llm bool) error {
	if _, err := os.Stat(cfg.SessionPath); err != nil {
		return fmt.Errorf("no session at %s", cfg.SessionPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("telegram: %w", err)
		}
		if !status.Authorized {
			return fmt.Errorf("session %s is not logged in (run tglogin -qr)", cfg.SessionPath)
		}
		fmt.Printf("OK  Telegram: logged in as %s %s (@%s) with %s\n", status.User.FirstName, status.User.LastName, status.User.Username, cfg.SessionPath)
		seen, err := chatIDs(ctx, client.API())
		if err != nil {
			return err
		}
		for _, id := range cfg.ChatIDs {
			if title, ok := seen[id]; ok {
				fmt.Printf("OK  Chat %d: %s\n", id, title)
			} else {
				return fmt.Errorf("chat %d not among your groups (TG_CHAT_IDS)", id)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if llm {
		if cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
			return fmt.Errorf("LLM: TG_LLM_API_KEY and TG_LLM_MODEL are required")
		}
		r := tgsignal.NewOpenAIReader(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMReason)
		if err := r.CheckModel(ctx); err != nil {
			return fmt.Errorf("LLM: %w", err)
		}
		fmt.Printf("OK  LLM: %s served at %s, key accepted\n", cfg.LLMModel, cfg.LLMBaseURL)
	}
	return nil
}

// chatIDs maps the user's group/channel ids to titles (first 100 dialogs).
func chatIDs(ctx context.Context, api *tg.Client) (map[int64]string, error) {
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100})
	if err != nil {
		return nil, err
	}
	var chats []tg.ChatClass
	switch r := res.(type) {
	case *tg.MessagesDialogs:
		chats = r.Chats
	case *tg.MessagesDialogsSlice:
		chats = r.Chats
	}
	out := map[int64]string{}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Channel:
			out[c.ID] = c.Title
		case *tg.Chat:
			out[c.ID] = c.Title
		}
	}
	return out, nil
}
