package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/analyst"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	cfg := analyst.LoadConfig()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("[analyst] invalid config: %v", err)
	}
	log.Printf("[analyst] enabled TFs: %v, tokens: %v", cfg.EnabledTFs, cfg.SubscribeTokenKeys)

	svc, err := analyst.New(cfg)
	if err != nil {
		log.Fatalf("[analyst] init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := svc.Run(ctx); err != nil {
		log.Fatalf("[analyst] fatal: %v", err)
	}
}
