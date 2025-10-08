# Chatych - Telegram LLM Bot

A Golang-based Telegram bot that uses OpenRouter to respond to messages in group chats with an LLM-based persona.

## Features

- Responds to group chat messages when mentioned or replied to
- Uses OpenRouter API for LLM integration
- Configurable persona and model selection
- Clean, modular architecture

## Prerequisites

- Go 1.21 or higher
- Telegram Bot Token (from [@BotFather](https://t.me/botfather))
- OpenRouter API Key (from [openrouter.ai](https://openrouter.ai/))

## Setup

1. **Clone the repository and install dependencies:**
   ```bash
   go mod download
   ```

2. **Create a `.env` file from the example:**
   ```bash
   cp .env.example .env
   ```

3. **Configure your `.env` file:**
   - `TELEGRAM_BOT_TOKEN`: Your bot token from @BotFather
   - `OPENROUTER_API_KEY`: Your API key from OpenRouter
   - `MODEL`: (Optional) LLM model to use (default: `anthropic/claude-3.5-sonnet`)

4. **Customize the bot persona (optional):**
   - Edit `config/persona.txt` to change the bot's personality
   - The persona is embedded into the binary at compile time

5. **Add the bot to your group:**
   - Search for your bot on Telegram
   - Add it to a group
   - Make sure the bot has permission to read messages

## Running

```bash
go run main.go
```

## Usage

The bot will respond when:
- It is mentioned with `@botusername` in a message
- Someone replies to one of its messages

## Project Structure

```
chatych/
├── main.go              # Entry point
├── config/
│   ├── config.go        # Configuration management
│   └── persona.txt      # Bot persona (embedded at compile time)
├── telegram/
│   └── bot.go           # Telegram bot logic
└── llm/
    └── client.go        # OpenRouter API client
```

## Available Models

You can use any model available on OpenRouter. Popular options include:
- `anthropic/claude-3.5-sonnet`
- `openai/gpt-4`
- `meta-llama/llama-3.1-70b-instruct`
- `google/gemini-pro`

See [OpenRouter's model list](https://openrouter.ai/models) for more options.
