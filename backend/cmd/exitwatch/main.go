package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/exitwatch"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	cfg := exitwatch.LoadConfig()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("[exitwatch] invalid config: %v", err)
	}

	svc, err := exitwatch.New(cfg)
	if err != nil {
		log.Fatalf("[exitwatch] init failed: %v", err)
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
		log.Fatalf("[exitwatch] fatal: %v", err)
	}
}
