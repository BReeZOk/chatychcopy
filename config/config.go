package config

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

//go:embed persona.txt
var personaPrompt string

//go:embed keywords.txt
var keywordsFile string

type Config struct {
	TelegramToken string
	OpenRouterKey string
	Model         string
	PersonaPrompt string
	Keywords      []string
}

func Load() (*Config, error) {
	// Load .env file if it exists
	_ = godotenv.Load()

	cfg := &Config{
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		OpenRouterKey: os.Getenv("OPENROUTER_API_KEY"),
		Model:         os.Getenv("MODEL"),
		PersonaPrompt: strings.TrimSpace(personaPrompt),
	}

	// Parse keywords from embedded keywords.txt (one per line)
	if keywordsFile != "" {
		lines := strings.Split(keywordsFile, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Skip empty lines and comments
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				cfg.Keywords = append(cfg.Keywords, strings.ToLower(trimmed))
			}
		}
	}

	// Validate required fields
	if cfg.TelegramToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.OpenRouterKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is required")
	}

	// Set defaults
	if cfg.Model == "" {
		cfg.Model = "anthropic/claude-3.5-sonnet"
	}

	return cfg, nil
}
