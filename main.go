package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"chatych/config"
	"chatych/telegram"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize Telegram bot
	bot, err := telegram.NewBot(cfg)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// Start bot
	log.Println("Starting bot...")
	go bot.Start()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down bot...")
	bot.Stop()
}
