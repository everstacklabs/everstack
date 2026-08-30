// Package telegram implements the Telegram messaging platform connector.
package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/channels"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"gopkg.in/telebot.v3"
)

// Connector implements channels.Connector for Telegram.
type Connector struct {
	bot                *telebot.Bot
	config             channels.ConnectorConfig
	handler            channels.MessageHandler
	interactionHandler channels.InteractionHandler
	status             atomic.Value

	allowedChats map[int64]bool
}

// NewConnector creates a new Telegram connector.
func NewConnector(cfg channels.ConnectorConfig, handler channels.MessageHandler) (*Connector, error) {
	token, ok := cfg.Credentials["bot_token"].(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("telegram: bot_token not found in credentials")
	}

	pref := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 30 * time.Second},
	}

	bot, err := telebot.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("telegram: create bot: %w", err)
	}

	c := &Connector{
		bot:          bot,
		config:       cfg,
		handler:      handler,
		allowedChats: make(map[int64]bool),
	}
	c.status.Store(channels.StatusDisconnected)

	// Parse allowed chat IDs
	if chats, ok := cfg.PlatformConfig["allowed_chats"].([]interface{}); ok {
		for _, ch := range chats {
			switch v := ch.(type) {
			case float64:
				c.allowedChats[int64(v)] = true
			case int64:
				c.allowedChats[v] = true
			}
		}
	}

	return c, nil
}

// Factory returns a ConnectorFactory for Telegram.
func Factory(cfg channels.ConnectorConfig, handler channels.MessageHandler) (channels.Connector, error) {
	return NewConnector(cfg, handler)
}

func (c *Connector) Start(ctx context.Context) error {
	c.status.Store(channels.StatusConnecting)

	// Register message handler
	c.bot.Handle(telebot.OnText, func(tbCtx telebot.Context) error {
		return c.handleText(ctx, tbCtx)
	})

	// Register callback query handler for HITL inline buttons
	c.bot.Handle(telebot.OnCallback, func(tbCtx telebot.Context) error {
		return c.handleCallback(ctx, tbCtx)
	})

	c.status.Store(channels.StatusConnected)
	logger.WithFields("channel", c.config.Name).Info("telegram: connected")

	// Start polling (blocks until stopped)
	go func() {
		<-ctx.Done()
		c.bot.Stop()
	}()

	c.bot.Start()
	return nil
}

func (c *Connector) Stop(_ context.Context) error {
	c.status.Store(channels.StatusDisconnected)
	c.bot.Stop()
	return nil
}

func (c *Connector) Send(_ context.Context, channelRef string, _ string, msg channels.OutboundMessage) (string, error) {
	chatID, err := parseChatID(channelRef)
	if err != nil {
		return "", err
	}

	chat := &telebot.Chat{ID: chatID}

	// Format with Telegram HTML
	text := msg.Text
	if len(msg.Embeds) > 0 {
		var sb strings.Builder
		if text != "" {
			sb.WriteString(text)
			sb.WriteString("\n\n")
		}
		for _, e := range msg.Embeds {
			if e.Title != "" {
				sb.WriteString("<b>")
				sb.WriteString(e.Title)
				sb.WriteString("</b>\n")
			}
			if e.Description != "" {
				sb.WriteString(e.Description)
				sb.WriteString("\n")
			}
			if e.CodeBlock != "" {
				sb.WriteString("<pre>")
				sb.WriteString(e.CodeBlock)
				sb.WriteString("</pre>\n")
			}
			for _, f := range e.Fields {
				sb.WriteString("<b>")
				sb.WriteString(f.Name)
				sb.WriteString(":</b> ")
				sb.WriteString(f.Value)
				sb.WriteString("\n")
			}
		}
		text = sb.String()
	}

	var opts []interface{}
	opts = append(opts, telebot.ModeHTML)

	// Add inline keyboard buttons for HITL approval
	if len(msg.Actions) > 0 {
		var buttons []telebot.InlineButton
		for _, action := range msg.Actions {
			buttons = append(buttons, telebot.InlineButton{
				Text:   action.Label,
				Unique: action.ID,
			})
		}
		markup := &telebot.ReplyMarkup{}
		markup.InlineKeyboard = [][]telebot.InlineButton{buttons}
		opts = append(opts, markup)
	}

	sent, err := c.bot.Send(chat, text, opts...)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d", sent.ID), nil
}

func (c *Connector) SendTyping(_ context.Context, channelRef string) error {
	chatID, err := parseChatID(channelRef)
	if err != nil {
		return err
	}
	return c.bot.Notify(&telebot.Chat{ID: chatID}, telebot.Typing)
}

func (c *Connector) EditMessage(_ context.Context, _ string, messageRef string, msg channels.OutboundMessage) error {
	// Telegram edit requires the original message object; simplified here
	// In production, we'd cache sent messages for editing
	_ = messageRef
	_ = msg
	return nil
}

func (c *Connector) Status() channels.ConnectorStatus {
	return c.status.Load().(channels.ConnectorStatus)
}

func (c *Connector) Platform() channels.Platform {
	return channels.PlatformTelegram
}

// SetInteractionHandler registers a handler for inline button clicks (HITL approve/deny).
func (c *Connector) SetInteractionHandler(h channels.InteractionHandler) {
	c.interactionHandler = h
}

func (c *Connector) handleCallback(ctx context.Context, tbCtx telebot.Context) error {
	cb := tbCtx.Callback()
	if cb == nil {
		return nil
	}

	if c.interactionHandler == nil {
		return tbCtx.Respond(&telebot.CallbackResponse{Text: "Not configured"})
	}

	sender := cb.Sender
	userName := ""
	userID := ""
	if sender != nil {
		userName = sender.Username
		if userName == "" {
			userName = strings.TrimSpace(sender.FirstName + " " + sender.LastName)
		}
		userID = fmt.Sprintf("%d", sender.ID)
	}

	msgRef := ""
	if cb.Message != nil {
		msgRef = fmt.Sprintf("%d", cb.Message.ID)
	}

	interaction := channels.Interaction{
		Platform:           channels.PlatformTelegram,
		ChannelConfigID:    c.config.ID,
		PlatformChannelRef: fmt.Sprintf("%d", tbCtx.Chat().ID),
		PlatformUserID:     userID,
		PlatformUserName:   userName,
		ActionID:           cb.Data,
		MessageRef:         msgRef,
	}

	if err := c.interactionHandler(ctx, interaction); err != nil {
		logger.WithError(err).Error("telegram: interaction handler failed")
		return tbCtx.Respond(&telebot.CallbackResponse{Text: "Failed"})
	}

	return tbCtx.Respond(&telebot.CallbackResponse{Text: "Action processed"})
}

func (c *Connector) handleText(ctx context.Context, tbCtx telebot.Context) error {
	sender := tbCtx.Sender()
	if sender == nil || sender.IsBot {
		return nil
	}

	chatID := tbCtx.Chat().ID

	// Check allowed chats
	if len(c.allowedChats) > 0 && !c.allowedChats[chatID] {
		return nil
	}

	text := strings.TrimSpace(tbCtx.Text())

	// Check mention prefix
	if prefix, ok := c.config.PlatformConfig["mention_prefix"].(string); ok && prefix != "" {
		if !strings.HasPrefix(text, prefix) {
			// In group chats, require prefix. In private chats, don't.
			if tbCtx.Chat().Type != telebot.ChatPrivate {
				return nil
			}
		}
		text = strings.TrimPrefix(text, prefix)
		text = strings.TrimSpace(text)
	}

	if text == "" {
		return nil
	}

	// Detect reply-to-thread context
	threadRef := ""
	if tbCtx.Message().ReplyTo != nil {
		threadRef = fmt.Sprintf("%d", tbCtx.Message().ReplyTo.ID)
	}

	userName := sender.Username
	if userName == "" {
		userName = strings.TrimSpace(sender.FirstName + " " + sender.LastName)
	}

	msg := channels.InboundMessage{
		Platform:           channels.PlatformTelegram,
		ChannelConfigID:    c.config.ID,
		AgentID:            c.config.AgentID,
		TenantID:           c.config.TenantID,
		PlatformChannelRef: fmt.Sprintf("%d", chatID),
		PlatformUserID:     fmt.Sprintf("%d", sender.ID),
		PlatformUserName:   userName,
		PlatformThreadRef:  threadRef,
		Text:               text,
		SessionMode:        c.config.SessionMode,
	}

	if err := c.handler(ctx, msg); err != nil {
		logger.WithFields("channel", c.config.Name, "user", userName).WithError(err).
			Error("telegram: failed to handle message")
	}

	return nil
}

func parseChatID(ref string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(ref, "%d", &id)
	return id, err
}
