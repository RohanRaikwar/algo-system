package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/stratengine"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[stratengine] starting...")

	cfg := stratengine.LoadConfig()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("[stratengine] invalid config: %v", err)
	}

	svc, err := stratengine.New(cfg)
	if err != nil {
		log.Fatalf("[stratengine] init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[stratengine] shutdown signal received")
		cancel()
	}()

	if err := svc.Run(ctx); err != nil {
		log.Fatalf("[stratengine] error: %v", err)
	}
}
