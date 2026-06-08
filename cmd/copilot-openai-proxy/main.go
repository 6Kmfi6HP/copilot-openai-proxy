package main

import (
	"context"
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

	client, err := copilot.NewClient(copilot.ClientConfig{
		MaxSessions:    cfg.MaxSessions,
		WarmSessions:   cfg.WarmSessions,
		SessionTTL:     time.Duration(cfg.SessionTTL) * time.Second,
		CleanupInt:     time.Duration(cfg.CleanupInterval) * time.Second,
		ConnTimeout:    time.Duration(cfg.ConnTimeout) * time.Second,
		Timeout:        time.Duration(cfg.Timeout) * time.Second,
		WSReadTimeout:  time.Duration(cfg.WSReadTimeout) * time.Second,
		WSWriteTimeout: time.Duration(cfg.WSWriteTimeout) * time.Second,
		WSPingInterval: time.Duration(cfg.WSPingInterval) * time.Second,
		Debug:          cfg.Debug,
		TimeZone:       cfg.TimeZone,
		ProxyURL:       cfg.ProxyURL,
		StartURL:       cfg.CopilotStartURL,
		WSURL:          cfg.CopilotWSURL,
	})
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("server shutdown failed: %v", err)
	}
}
