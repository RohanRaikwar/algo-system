package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/mdengine"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	cfg := mdengine.LoadConfig()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("[mdengine] invalid config: %v", err)
	}

	svc, err := mdengine.New(cfg)
	if err != nil {
		log.Fatalf("[mdengine] init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[mdengine] shutdown signal received")
		cancel()
	}()

	if err := svc.Run(ctx); err != nil {
		log.Fatalf("[mdengine] error: %v", err)
	}
}
