package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/tgsignal"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	cfg := tgsignal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("[tgsignal] invalid config: %v", err)
	}

	svc, err := tgsignal.New(cfg)
	if err != nil {
		log.Fatalf("[tgsignal] init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := svc.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("[tgsignal] fatal: %v", err)
	}
}
