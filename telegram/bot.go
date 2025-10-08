package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"chatych/config"
	"chatych/llm"

	"math/rand"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
)

const (
	maxHistory         = 30              // how many messages we keep in memory per chat
	triggerCooldownSec = 10              // anti-spam: no more than once every N seconds in one chat
)

var reactionEmojis = []string{"👍", "❤️", "😂", "🔥", "🤔", "🎉", "💯", "🤣", "🤯", "💩", "🤮", "🤡", "👎"}

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

	// anti-repeat and anti-spam
	lastBotReply   map[int64]string      // chatID -> the bot's last response
	lastTriggerTs  map[int64]time.Time   // chatID -> last trigger time
	lastHandledMsgID  map[int64]int // 

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
		lastBotReply:   make(map[int64]string),
		lastTriggerTs:  make(map[int64]time.Time),
		lastHandledMsgID: make(map[int64]int), // <<< added
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
		// Message from our bot - assistant role
		message = llm.Message{
			Role:    "assistant",
			Content: msg.Text,
		}
	} else {
		// Message from a person
		userName := msg.From.FirstName
		if userName == "" {
			userName = msg.From.Username
		}
		// quick fix for spaces
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
	return nil
}

func (b *Bot) recentlyTriggered(chatID int64) bool {
	last, ok := b.lastTriggerTs[chatID]
	if !ok {
		return false
	}
	return time.Since(last) < time.Duration(triggerCooldownSec)*time.Second
}

func (b *Bot) markTriggered(chatID int64) {
	b.lastTriggerTs[chatID] = time.Now()
}

func (b *Bot) messageFilter(msg *gotgbot.Message) bool {
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return false
	}
	if msg.Text == "" {
		return false
	}
	if msg.From.Id == b.botUser.Id {
		return false
	}

	// Triggers: bot mention, bot reply, keywords
	botUsername := "@" + b.botUser.Username
	isMentioned := strings.Contains(strings.ToLower(msg.Text), strings.ToLower(botUsername))
	isReplyToBot := msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.Id == b.botUser.Id

	hasKeyword := false
	if len(b.config.Keywords) > 0 {
		lowerText := strings.ToLower(msg.Text)
		for _, keyword := range b.config.Keywords {
			if strings.Contains(lowerText, strings.ToLower(keyword)) {
				hasKeyword = true
				break
			}
		}
	}

	should := isMentioned || isReplyToBot || hasKeyword
	if !should {
		return false
	}

	// anti-spam: chat cooldown
	//if b.recentlyTriggered(msg.Chat.Id) {
		//return false
	//}

	return true
}

func (b *Bot) handleMessage(bot *gotgbot.Bot, ctx *ext.Context) error {
    msg := ctx.EffectiveMessage
    chatID := msg.Chat.Id

    // already responded to this update? leaving
    if lastID, ok := b.lastHandledMsgID[chatID]; ok && lastID == msg.MessageId {
        return nil
    }

    b.markTriggered(chatID)
    // ... Reading historyCopy as you do ...

    // ---------- 1) Select the target message ----------
    var targetMessageText string
    var targetAuthorName string

    triggerMessage := ctx.EffectiveMessage

    cleanedText := strings.ToLower(triggerMessage.Text)
    cleanedText = strings.ReplaceAll(cleanedText, "@"+strings.ToLower(b.botUser.Username), "")
    for _, keyword := range b.config.Keywords {
        cleanedText = strings.ReplaceAll(cleanedText, strings.ToLower(keyword), "")
    }
    cleanedText = strings.TrimSpace(cleanedText)

    switch {
    // A) responded with a reply to a specific message
    case triggerMessage.ReplyToMessage != nil && triggerMessage.ReplyToMessage.Text != "":
        targetMessageText = triggerMessage.ReplyToMessage.Text
        if triggerMessage.ReplyToMessage.From != nil {
            targetAuthorName = triggerMessage.ReplyToMessage.From.FirstName
            if targetAuthorName == "" {
                targetAuthorName = triggerMessage.ReplyToMessage.From.Username
            }
        } else {
            targetAuthorName = "Someone"
        }

    // B) empty trigger - take the LAST HUMAN message before the current one
    case cleanedText == "" && len(historyCopy) > 1:
        // the last element of the story is the trigger itself (user)
        if author, text, ok := lastUserMsg(historyCopy, len(historyCopy)-2); ok {
            targetAuthorName = author
            targetMessageText = text
        } else {
            // fallback: the trigger itself
            targetAuthorName = triggerMessage.From.FirstName
            if targetAuthorName == "" {
                targetAuthorName = triggerMessage.From.Username
            }
            targetMessageText = triggerMessage.Text
        }

    // C) regular message - we reply to it
    default:
        targetMessageText = triggerMessage.Text
        targetAuthorName = triggerMessage.From.FirstName
        if targetAuthorName == "" {
            targetAuthorName = triggerMessage.From.Username
        }
    }

    // ---------- 2) LLM instruction----------
    instruction := fmt.Sprintf(
        "Ты — «Чатыч». Ответь на конкретное сообщение пользователя '%s': «%s». "+
            "Используй недавний контекст, но отвечай по сути именно на это сообщение. Будь краток.",
        targetAuthorName, targetMessageText,
    )
    historyCopy = append(historyCopy, llm.Message{Role: "user", Content: instruction})

    // ---------- 3) LLM's reply ----------
    response, err := b.llm.GetResponse(b.ctx, historyCopy)
    if err != nil {
        log.Printf("Error getting LLM response: %v", err)
        return err
    }

    // remove possible prefix
    botNamePrefix := "чатыч:"
    if strings.HasPrefix(strings.ToLower(response), botNamePrefix) {
        response = strings.TrimSpace(response[len(botNamePrefix):])
    }

    // ---------- 4) Anti-repeat ----------
    if last, ok := b.lastBotReply[chatID]; ok &&
        strings.EqualFold(strings.TrimSpace(last), strings.TrimSpace(response)) {
        // Don't send the same text twice.
        return nil
    }

    // ---------- 5) Sending and committing the processed messageId ----------
    _, err = msg.Reply(bot, response, &gotgbot.SendMessageOpts{
        ReplyParameters: &gotgbot.ReplyParameters{MessageId: msg.MessageId},
    })
    if err != nil {
        log.Printf("Error sending message: %v", err)
        return err
    }

    b.lastBotReply[chatID] = response
    b.lastHandledMsgID[chatID] = msg.MessageId
    return nil
} // <<< IMPORTANT: Close handleMessage

func lastUserMsg(msgs []llm.Message, start int) (author, text string, ok bool) {
    // start — index from which we go back (inclusive)
    for i := start; i >= 0; i-- {
        m := msgs[i]
        if m.Role == "user" {
            parts := strings.SplitN(m.Content, ": ", 2)
            if len(parts) == 2 {
                return parts[0], parts[1], true
            }
            return "Someone", m.Content, true
        }
    }
    return "", "", false
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
        if err := b.updater.Stop(); err != nil {
            log.Printf("Error stopping updater: %v", err)
        }
    }
}
