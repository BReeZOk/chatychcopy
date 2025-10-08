# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Chatych is a Telegram bot that responds to group chat messages using LLM-based personas via the OpenRouter API. The bot responds when mentioned or when users reply to its messages.

## Architecture

The codebase follows a clean, modular architecture with three main packages:

- **config**: Handles configuration loading from `.env` and embeds `persona.txt` at compile time using `//go:embed`
- **telegram**: Contains all Telegram bot logic including message handling, mention detection, and reply context
- **llm**: OpenRouter API client that sends chat completions requests

### Key Flow
1. `main.go` loads config and starts the bot with graceful shutdown handling
2. `telegram/bot.go` polls for updates, filters group messages, checks for mentions/@username or replies
3. When triggered, extracts user's first+last name, builds context from replied-to messages, and calls LLM
4. `llm/client.go` combines persona prompt with user message (for Bedrock compatibility) and calls OpenRouter API

### Important Implementation Details
- The persona is embedded at build time from `config/persona.txt` (line 12-13 in config/config.go)
- User identification uses first name + last name (telegram/bot.go:88-94), falling back to username
- Reply context includes the bot's previous message with "{persona} previously said:" prefix (telegram/bot.go:102-104)
- The LLM client combines persona with user message in a single user role message rather than using system role (llm/client.go:52-55)

## Development Commands

### Build and Run
```bash
# Install dependencies
go mod download

# Run the bot
go run main.go

# Build binary
go build -o chatych .
```

### Docker
```bash
# Build image
docker build -t chatych .

# Run container (requires .env file)
docker run --env-file .env chatych
```

### Configuration
- Copy `.env.example` to `.env` and configure:
  - `TELEGRAM_BOT_TOKEN`: From @BotFather
  - `OPENROUTER_API_KEY`: From openrouter.ai
  - `MODEL`: Optional, defaults to `anthropic/claude-3.5-sonnet`
- Edit `config/persona.txt` to change bot personality (requires rebuild)

## Testing the Bot
1. Add bot to a Telegram group
2. Ensure bot has permission to read messages
3. Mention bot with `@botusername` or reply to one of its messages
4. Check logs for message processing: `[ChatTitle] Username: message text`
