package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/archiver"
	"trading-systemv1/internal/markethours"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	// A bad config disables archiving instead of exiting: the container
	// entrypoint stops every trading service when any one process dies.
	cfg := archiver.LoadConfig()
	if err := cfg.Validate(); err != nil {
		log.Printf("[archiver] ❌ invalid config, archiving disabled: %v", err)
		cfg.Enabled = false
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Holidays decide the trading-window guard.
	configDir := markethours.GetConfigDir()
	markethours.InitHolidays(configDir)
	markethours.StartBackgroundRefresher(ctx, configDir)

	if err := archiver.New(cfg).Run(ctx); err != nil {
		log.Printf("[archiver] ❌ %v; idle", err)
		<-ctx.Done()
	}
}
