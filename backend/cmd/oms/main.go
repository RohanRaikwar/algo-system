// Command oms is the standalone order-management process. In phase 3 it
// runs in paper/shadow mode alongside stratengine; see internal/omsvc.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/omsvc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	svc, err := omsvc.New(omsvc.LoadConfig())
	if err != nil {
		log.Fatalf("[oms] init failed: %v", err)
	}
	defer svc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := svc.Run(ctx); err != nil {
		log.Printf("[oms] stopped: %v", err)
		os.Exit(1)
	}
}
