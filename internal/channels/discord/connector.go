// Package discord implements the Discord messaging platform connector.
package discord

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
	"github.com/everstacklabs/everstack/internal/channels"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Connector implements channels.Connector for Discord.
type Connector struct {
	session            *discordgo.Session
	config             channels.ConnectorConfig
	handler            channels.MessageHandler
	interactionHandler channels.InteractionHandler
	botID              string
	status             atomic.Value // channels.ConnectorStatus

	// Allowed channel IDs (empty = all channels)
	allowedChannels map[string]bool
	mentionPrefix   string
}

// NewConnector creates a new Discord connector.
func NewConnector(cfg channels.ConnectorConfig, handler channels.MessageHandler) (*Connector, error) {
	token, ok := cfg.Credentials["bot_token"].(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("discord: bot_token not found in credentials")
	}

	// Ensure "Bot " prefix
	if !strings.HasPrefix(token, "Bot ") {
		token = "Bot " + token
	}

	session, err := discordgo.New(token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuildMessageTyping

	c := &Connector{
		session:         session,
		config:          cfg,
		handler:         handler,
		allowedChannels: make(map[string]bool),
	}
	c.status.Store(channels.StatusDisconnected)

	// Parse allowed channels from platform config
	if chans, ok := cfg.PlatformConfig["allowed_channels"].([]interface{}); ok {
		for _, ch := range chans {
			if s, ok := ch.(string); ok {
				c.allowedChannels[s] = true
			}
		}
	}

	// Parse mention prefix
	if prefix, ok := cfg.PlatformConfig["mention_prefix"].(string); ok {
		c.mentionPrefix = prefix
	}

	return c, nil
}

// Factory returns a ConnectorFactory for Discord.
func Factory(cfg channels.ConnectorConfig, handler channels.MessageHandler) (channels.Connector, error) {
	return NewConnector(cfg, handler)
}

// Start connects to Discord and begins listening for messages.
func (c *Connector) Start(ctx context.Context) error {
	c.status.Store(channels.StatusConnecting)

	// Register message handler
	c.session.AddHandler(c.onMessageCreate)
	// Register interaction handler for HITL buttons
	c.session.AddHandler(c.onInteractionCreate)

	if err := c.session.Open(); err != nil {
		c.status.Store(channels.StatusError)
		return fmt.Errorf("discord: open session: %w", err)
	}

	c.botID = c.session.State.User.ID
	c.status.Store(channels.StatusConnected)

	logger.WithFields("bot_id", c.botID, "channel", c.config.Name).
		Info("discord: connected")

	// Wait for context cancellation
	<-ctx.Done()

	return nil
}

// Stop gracefully disconnects from Discord.
func (c *Connector) Stop(_ context.Context) error {
	c.status.Store(channels.StatusDisconnected)
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// Send sends a message to a Discord channel.
func (c *Connector) Send(_ context.Context, channelRef string, threadRef string, msg channels.OutboundMessage) (string, error) {
	targetChannel := channelRef
	if threadRef != "" {
		targetChannel = threadRef
	}

	// Handle rich embeds
	if len(msg.Embeds) > 0 {
		embeds := make([]*discordgo.MessageEmbed, 0, len(msg.Embeds))
		for _, e := range msg.Embeds {
			embed := &discordgo.MessageEmbed{
				Title:       e.Title,
				Description: e.Description,
				Color:       e.Color,
			}
			for _, f := range e.Fields {
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
					Name:   f.Name,
					Value:  f.Value,
					Inline: f.Inline,
				})
			}
			if e.CodeBlock != "" {
				embed.Description += "\n```\n" + e.CodeBlock + "\n```"
			}
			embeds = append(embeds, embed)
		}

		sent, err := c.session.ChannelMessageSendComplex(targetChannel, &discordgo.MessageSend{
			Content: msg.Text,
			Embeds:  embeds,
		})
		if err != nil {
			return "", err
		}
		return sent.ID, nil
	}

	// If action buttons are present, send as complex message with components
	if len(msg.Actions) > 0 {
		var buttons []discordgo.MessageComponent
		for _, action := range msg.Actions {
			style := discordgo.PrimaryButton
			switch action.Style {
			case "danger":
				style = discordgo.DangerButton
			case "secondary":
				style = discordgo.SecondaryButton
			}
			buttons = append(buttons, discordgo.Button{
				Label:    action.Label,
				Style:    style,
				CustomID: action.ID,
			})
		}

		sent, err := c.session.ChannelMessageSendComplex(targetChannel, &discordgo.MessageSend{
			Content: msg.Text,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: buttons},
			},
		})
		if err != nil {
			return "", err
		}
		return sent.ID, nil
	}

	sent, err := c.session.ChannelMessageSend(targetChannel, msg.Text)
	if err != nil {
		return "", err
	}
	return sent.ID, nil
}

// SendTyping sends a typing indicator to a Discord channel.
func (c *Connector) SendTyping(_ context.Context, channelRef string) error {
	return c.session.ChannelTyping(channelRef)
}

// EditMessage edits a previously sent Discord message.
func (c *Connector) EditMessage(_ context.Context, channelRef string, messageRef string, msg channels.OutboundMessage) error {
	_, err := c.session.ChannelMessageEdit(channelRef, messageRef, msg.Text)
	return err
}

// Status returns the current connection status.
func (c *Connector) Status() channels.ConnectorStatus {
	return c.status.Load().(channels.ConnectorStatus)
}

// Platform returns the platform type.
func (c *Connector) Platform() channels.Platform {
	return channels.PlatformDiscord
}

// SetInteractionHandler registers a handler for button clicks (HITL approve/deny).
func (c *Connector) SetInteractionHandler(h channels.InteractionHandler) {
	c.interactionHandler = h
}

// onInteractionCreate handles Discord interaction events (button clicks).
func (c *Connector) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if c.interactionHandler == nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Interaction handling not configured.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	userName := ""
	if i.Member != nil && i.Member.User != nil {
		userName = i.Member.User.Username
	} else if i.User != nil {
		userName = i.User.Username
	}

	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	interaction := channels.Interaction{
		Platform:           channels.PlatformDiscord,
		ChannelConfigID:    c.config.ID,
		PlatformChannelRef: i.ChannelID,
		PlatformUserID:     userID,
		PlatformUserName:   userName,
		ActionID:           data.CustomID,
		MessageRef:         i.Message.ID,
	}

	ctx := context.Background()
	if err := c.interactionHandler(ctx, interaction); err != nil {
		logger.WithError(err).Error("discord: interaction handler failed")
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Failed to process action.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Action processed: " + data.CustomID,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// ListPlatformChannels implements channels.ChannelLister.
// It lists all text channels from guilds the bot has access to.
// Uses the gateway state cache (populated from READY/GUILD_CREATE events)
// instead of the REST UserGuilds endpoint which requires OAuth2 scopes.
func (c *Connector) ListPlatformChannels(_ context.Context) ([]channels.PlatformChannel, error) {
	if c.Status() != channels.StatusConnected {
		return nil, fmt.Errorf("discord: connector not connected")
	}

	// Read guilds from gateway state cache — avoids REST 403 issues
	c.session.State.RLock()
	guilds := make([]*discordgo.Guild, len(c.session.State.Guilds))
	copy(guilds, c.session.State.Guilds)
	c.session.State.RUnlock()

	var result []channels.PlatformChannel
	for _, g := range guilds {
		guildChannels, err := c.session.GuildChannels(g.ID)
		if err != nil {
			logger.WithFields("guild", g.Name, "guild_id", g.ID).WithError(err).
				Warn("discord: failed to list channels for guild")
			continue
		}
		for _, ch := range guildChannels {
			if ch.Type != discordgo.ChannelTypeGuildText && ch.Type != discordgo.ChannelTypeGuildNews {
				continue
			}
			result = append(result, channels.PlatformChannel{
				ID:   ch.ID,
				Name: g.Name + " / #" + ch.Name,
				Type: "text",
			})
		}
	}

	return result, nil
}

// onMessageCreate handles Discord MessageCreate events.
func (c *Connector) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages
	if m.Author.ID == c.botID {
		return
	}

	// Ignore other bots
	if m.Author.Bot {
		return
	}

	// Check allowed channels
	if len(c.allowedChannels) > 0 && !c.allowedChannels[m.ChannelID] {
		return
	}

	// Check if bot is mentioned or prefix matches
	text := m.Content
	isMentioned := false

	for _, mention := range m.Mentions {
		if mention.ID == c.botID {
			isMentioned = true
			// Remove the mention from the text
			text = strings.ReplaceAll(text, "<@"+c.botID+">", "")
			text = strings.ReplaceAll(text, "<@!"+c.botID+">", "")
			text = strings.TrimSpace(text)
			break
		}
	}

	// Check prefix
	if !isMentioned && c.mentionPrefix != "" {
		if strings.HasPrefix(text, c.mentionPrefix) {
			text = strings.TrimPrefix(text, c.mentionPrefix)
			text = strings.TrimSpace(text)
			isMentioned = true
		}
	}

	// In DMs, no mention needed. When a default agent is assigned, respond to all messages.
	isDM := m.GuildID == ""
	hasDefaultAgent := c.config.AgentID != ""
	if !isMentioned && !isDM && !hasDefaultAgent {
		return
	}

	if text == "" {
		return
	}

	// Detect thread context
	threadRef := ""
	if m.Thread != nil {
		threadRef = m.Thread.ID
	}
	// If message is in a thread, the channel ID is the thread ID
	ch, err := s.Channel(m.ChannelID)
	if err == nil && ch.IsThread() {
		threadRef = m.ChannelID
	}

	msg := channels.InboundMessage{
		Platform:           channels.PlatformDiscord,
		ChannelConfigID:    c.config.ID,
		AgentID:            c.config.AgentID,
		TenantID:           c.config.TenantID,
		PlatformChannelRef: m.ChannelID,
		PlatformUserID:     m.Author.ID,
		PlatformUserName:   m.Author.Username,
		PlatformThreadRef:  threadRef,
		Text:               text,
		SessionMode:        c.config.SessionMode,
	}

	// Parse attachments
	for _, a := range m.Attachments {
		msg.Attachments = append(msg.Attachments, channels.Attachment{
			URL:      a.URL,
			Filename: a.Filename,
			MimeType: a.ContentType,
			Size:     int64(a.Size),
		})
	}

	ctx := context.Background()
	if err := c.handler(ctx, msg); err != nil {
		logger.WithFields("channel", c.config.Name, "user", m.Author.Username).WithError(err).
			Error("discord: failed to handle message")
	}
}
