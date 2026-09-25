package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/api"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	addr := config.GetEnv("API_ADDR", ":8081")
	srv := &http.Server{
		Addr:    addr,
		Handler: api.NewRouter(),
	}

	go func() {
		log.Printf("[api] serving on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[api] server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[api] graceful shutdown error: %v", err)
	}
	log.Println("[api] shutdown complete")
}
