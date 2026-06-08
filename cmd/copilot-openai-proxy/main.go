package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"copilot-openai-proxy/internal/config"
	"copilot-openai-proxy/internal/copilot"
	"copilot-openai-proxy/internal/openai"
	"copilot-openai-proxy/internal/server"
)

func main() {
	cfg := config.Load()

	client, err := copilot.NewClient(
		cfg.MaxSessions,
		time.Duration(cfg.SessionTTL)*time.Second,
		time.Duration(cfg.CleanupInterval)*time.Second,
		time.Duration(cfg.ConnTimeout)*time.Second,
		time.Duration(cfg.Timeout)*time.Second,
		cfg.Debug,
		cfg.TimeZone,
	)
	if err != nil {
		log.Fatalf("failed to create copilot client: %v", err)
	}

	handler := openai.NewHandler(client)

	srv := server.New(handler, cfg)
	log.Printf("starting copilot openai proxy addr=%s auth_enabled=%v", cfg.Addr, cfg.APIKey != "")

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
}