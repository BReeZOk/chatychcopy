package telegram

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"chatych/config"
	"chatych/llm"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
)

const maxHistory = 10

var reactionEmojis = []string{"👍", "❤️", "😂", "🔥", "🤔", "🎉", "💯", "🤣", "🤯", "🐳"}

type Bot struct {
	bot            *gotgbot.Bot
	llm            *llm.Client
	config         *config.Config
	ctx            context.Context
	cancel         context.CancelFunc
	updater        *ext.Updater
	botUser        *gotgbot.User
	messageHistory map[int64][]llm.Message
	historyMutex   sync.RWMutex
}

func NewBot(cfg *config.Config) (*Bot, error) {
	bot, err := gotgbot.NewBot(cfg.TelegramToken, &gotgbot.BotOpts{
		RequestOpts: &gotgbot.RequestOpts{
			Timeout: 30 * time.Second,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	botUser, err := bot.GetMe(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get bot info: %w", err)
	}
	log.Printf("Authorized on account %s", botUser.Username)

	llmClient := llm.NewClient(cfg.OpenRouterKey, cfg.Model, cfg.PersonaPrompt)

	// NB: rand.New(...) ниже ничего не делает, т.к. результат не сохраняется.
	// Вероятно, хотелось посеять глобальный генератор:
	// rand.Seed(time.Now().UnixNano())
	rand.New(rand.NewSource(time.Now().UnixNano()))

	ctx, cancel := context.WithCancel(context.Background())

	return &Bot{
		bot:            bot,
		llm:            llmClient,
		config:         cfg,
		ctx:            ctx,
		cancel:         cancel,
		botUser:        botUser,
		messageHistory: make(map[int64][]llm.Message),
	}, nil
}

func (b *Bot) Start() {
	dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(bot *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
			log.Printf("Update error: %v", err)
			return ext.DispatcherActionNoop
		},
		MaxRoutines: ext.DefaultMaxRoutines,
	})

	// Group -1: Message logger (runs first)
	dispatcher.AddHandlerToGroup(handlers.NewMessage(b.logMessageFilter, b.handleLogMessage), -1)

	// Group 0: Main logic
	dispatcher.AddHandler(handlers.NewMessage(b.reactionFilter, b.handleReaction))
	dispatcher.AddHandler(handlers.NewMessage(b.messageFilter, b.handleMessage))

	updater := ext.NewUpdater(dispatcher, nil)
	b.updater = updater

	err := updater.StartPolling(b.bot, &ext.PollingOpts{
		DropPendingUpdates: false,
		GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
			Timeout: 60,
			RequestOpts: &gotgbot.RequestOpts{
				Timeout: 61 * time.Second,
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to start polling: %v", err)
	}

	log.Printf("Bot started polling...")
	<-b.ctx.Done()
}

func (b *Bot) logMessageFilter(msg *gotgbot.Message) bool {
	return (msg.Chat.Type == "group" || msg.Chat.Type == "supergroup") && msg.Text != ""
}

func (b *Bot) handleLogMessage(bot *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	chatID := msg.Chat.Id

	var message llm.Message
	if msg.From.Id == b.botUser.Id {
		// It's a message from our bot, so use the 'assistant' role.
		message = llm.Message{
			Role:    "assistant",
			Content: msg.Text, // No need for a "Чатыч:" prefix here.
		}
	} else {
		// It's a message from a human user.
		userName := msg.From.FirstName
		if userName == "" {
			userName = msg.From.Username
		}
		// Quick fix for user names with spaces
		userName = strings.Split(userName, " ")[0]

		content := fmt.Sprintf("%s: %s", userName, msg.Text)
		message = llm.Message{
			Role:    "user",
			Content: content,
		}
	}

	b.historyMutex.Lock()
	defer b.historyMutex.Unlock()

	history := b.messageHistory[chatID]
	history = append(history, message)
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	b.messageHistory[chatID] = history

	return nil // Continue to next handlers
}

func (b *Bot) messageFilter(msg *gotgbot.Message) bool {
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return false
	}
	if msg.Text == "" {
		return false
	}

	botUsername := "@" + b.botUser.Username
	isMentioned := strings.Contains(msg.Text, botUsername)
	isReply := msg.ReplyToMessage != nil && msg.ReplyToMessage.From.Id == b.botUser.Id

	hasKeyword := false
	if len(b.config.Keywords) > 0 {
		lowerText := strings.ToLower(msg.Text)
		for _, keyword := range b.config.Keywords {
			if strings.Contains(lowerText, keyword) {
				hasKeyword = true
				break
			}
		}
	}

	return isMentioned || isReply || hasKeyword
}

func (b *Bot) handleMessage(bot *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	chatID := msg.Chat.Id

	log.Printf("[%s] %s: %s", msg.Chat.Title, msg.From.Username, msg.Text)

	b.historyMutex.RLock()
	history, ok := b.messageHistory[chatID]
	if !ok {
		b.historyMutex.RUnlock()
		log.Println("No history found for this chat.")
		return nil // Should not happen if logger works
	}

	// Create a copy of the history to avoid race conditions after unlocking
	historyCopy := make([]llm.Message, len(history))
	copy(historyCopy, history)
	b.historyMutex.RUnlock()

	// 1. Determine the actual message to respond to.
	var targetMessageText string
	var targetAuthorName string

	triggerMessage := ctx.EffectiveMessage
	isReply := triggerMessage.ReplyToMessage != nil && triggerMessage.ReplyToMessage.From.Id == b.botUser.Id

	// Clean the trigger message to see if it contains any actual content besides keywords/mentions.
	cleanedText := strings.ToLower(triggerMessage.Text)
	cleanedText = strings.ReplaceAll(cleanedText, "@"+strings.ToLower(b.botUser.Username), "")
	for _, keyword := range b.config.Keywords {
		cleanedText = strings.ReplaceAll(cleanedText, keyword, "")
	}
	cleanedText = strings.TrimSpace(cleanedText)

	// Scenario A: It's a direct reply to the bot. Target the message being replied to.
	if isReply {
		targetMessage := triggerMessage.ReplyToMessage
		targetMessageText = targetMessage.Text

		targetAuthorName = targetMessage.From.FirstName
		if targetAuthorName == "" {
			targetAuthorName = targetMessage.From.Username
		}
	} else if cleanedText == "" && len(historyCopy) > 1 {
		// Scenario B: Trigger is just a keyword (e.g., "чатыч"). Target the previous message in the history.
		// The last message in history is the trigger itself, so the one before it is our target.
		previousMessage := historyCopy[len(historyCopy)-2] // This is an llm.Message struct

		// The content is formatted as "Username: Message", so we need to parse it.
		parts := strings.SplitN(previousMessage.Content, ": ", 2)
		if len(parts) == 2 {
			targetAuthorName = parts[0]
			targetMessageText = parts[1]
		} else {
			// Fallback if parsing fails, which is unlikely.
			targetMessageText = previousMessage.Content
			targetAuthorName = "Someone"
		}
	} else {
		// Scenario C: It's a normal message containing content. Target the trigger message itself.
		targetMessage := triggerMessage
		targetMessageText = triggerMessage.Text // Use original text for the LLM

		targetAuthorName = targetMessage.From.FirstName
		if targetAuthorName == "" {
			targetAuthorName = targetMessage.From.Username
		}
	}

	// 2. Construct the final instruction for the LLM.
	instruction := fmt.Sprintf(
		"Ты — «Чатыч», отвечаешь на сообщение от пользователя '%s'. Его сообщение: «%s». "+
			"Используй контекст предыдущих сообщений, чтобы твой ответ был в тему, но отвечай именно на это сообщение.",
		targetAuthorName, targetMessageText,
	)

	historyCopy = append(historyCopy, llm.Message{Role: "user", Content: instruction})

	// 3. Get response from LLM.
	response, err := b.llm.GetResponse(b.ctx, historyCopy)
	if err != nil {
		log.Printf("Error getting LLM response: %v", err)
		// Consider sending an error message to the chat
		return err
	}

	botNamePrefix := "чатыч:"
	if strings.HasPrefix(strings.ToLower(response), botNamePrefix) {
		response = strings.TrimSpace(response[len(botNamePrefix):])
	}

	_, err = msg.Reply(bot, response, &gotgbot.SendMessageOpts{
		ReplyParameters: &gotgbot.ReplyParameters{MessageId: msg.MessageId},
	})
	if err != nil {
		log.Printf("Error sending message: %v", err)
		return err
	}

	return nil
}

func (b *Bot) reactionFilter(msg *gotgbot.Message) bool {
	if b.messageFilter(msg) {
		return false
	}
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return false
	}
	if msg.From.Id == b.botUser.Id {
		return false
	}
	return true
}

func (b *Bot) handleReaction(bot *gotgbot.Bot, ctx *ext.Context) error {
	if rand.Intn(100) < 15 {
		msg := ctx.EffectiveMessage
		randomEmoji := reactionEmojis[rand.Intn(len(reactionEmojis))]
		_, err := bot.SetMessageReaction(ctx.EffectiveChat.Id, msg.MessageId, &gotgbot.SetMessageReactionOpts{
			Reaction: []gotgbot.ReactionType{gotgbot.ReactionTypeEmoji{Emoji: randomEmoji}},
		})
		if err != nil {
			log.Printf("Error sending reaction: %v", err)
			return err
		}
	}
	return nil
}

func (b *Bot) Stop() {
	b.cancel()
	if b.updater != nil {
		err := b.updater.Stop()
		if err != nil {
			log.Printf("Error stopping updater: %v", err)
		}
	}
}
