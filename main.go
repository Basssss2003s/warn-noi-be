package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"discord-bot-dashboard/backend/internal/api"
	"discord-bot-dashboard/backend/internal/bot"
	"discord-bot-dashboard/backend/internal/config"
	"discord-bot-dashboard/backend/internal/music"
	"discord-bot-dashboard/backend/internal/store"
)

func main() {
	cfg := config.Load()

	fmt.Println("==================================================")
	fmt.Println("   🍧 หวานน้อย | WARN-NOI DISCORD BOT (GO CORE)")
	fmt.Println("==================================================")
	if cfg.IsDemoMode {
		log.Println("⚡ Running in DEMO MODE (No live token provided, full UI interactive simulation)")
	} else {
		log.Println("⚡ Running in LIVE MODE (Connecting to Discord API)")
	}

	// 1. Initialize data store
	st := store.New(cfg.DataPath)

	// 2. Initialize and start Discord Bot
	botService := bot.New(cfg, st)
	if err := botService.Start(); err != nil {
		log.Fatalf("Fatal error starting bot: %v", err)
	}
	defer botService.Stop()

	// Connect music engine with bot voice actions & persistent store
	music.SetHooks(
		botService.JoinVoice,
		botService.LeaveVoice,
		st.SaveMusicState,
		st.GetMusicState,
	)
	// Ensure audio microservice is running for Discord voice streaming
	go music.EnsureVoiceWorkerRunning()

	// 3. Initialize HTTP REST API Server
	server := api.NewServer(cfg, st, botService)
	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      server.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("🌐 Web Dashboard API listening on http://localhost:%s", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 4. Graceful shutdown handler
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	log.Println("👋 Server stopped safely.")
}
