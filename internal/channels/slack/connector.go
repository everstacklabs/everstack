// Package slack implements the Slack messaging platform connector.
package slack

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/channels"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Connector implements channels.Connector for Slack using Socket Mode.
type Connector struct {
	client             *slack.Client
	socketMod          *socketmode.Client
	config             channels.ConnectorConfig
	handler            channels.MessageHandler
	interactionHandler channels.InteractionHandler
	botUserID          string
	status             atomic.Value

	allowedChannels map[string]bool
}

// NewConnector creates a new Slack connector.
func NewConnector(cfg channels.ConnectorConfig, handler channels.MessageHandler) (*Connector, error) {
	botToken, ok := cfg.Credentials["bot_token"].(string)
	if !ok || botToken == "" {
		return nil, fmt.Errorf("slack: bot_token not found in credentials")
	}

	appToken, _ := cfg.Credentials["app_token"].(string)
	if appToken == "" {
		return nil, fmt.Errorf("slack: app_token not found in credentials (required for Socket Mode)")
	}

	// Check if debug mode is enabled via platform config
	debug, _ := cfg.PlatformConfig["debug"].(bool)

	clientOpts := []slack.Option{
		slack.OptionAppLevelToken(appToken),
	}
	if debug {
		clientOpts = append(clientOpts,
			slack.OptionDebug(true),
			slack.OptionLog(log.New(os.Stderr, "slack-api: ", log.LstdFlags)),
		)
	}
	client := slack.New(botToken, clientOpts...)

	smOpts := []socketmode.Option{}
	if debug {
		smOpts = append(smOpts,
			socketmode.OptionDebug(true),
			socketmode.OptionLog(log.New(os.Stderr, "slack-sm: ", log.LstdFlags)),
		)
	}
	socketClient := socketmode.New(client, smOpts...)

	c := &Connector{
		client:          client,
		socketMod:       socketClient,
		config:          cfg,
		handler:         handler,
		allowedChannels: make(map[string]bool),
	}
	c.status.Store(channels.StatusDisconnected)

	if chans, ok := cfg.PlatformConfig["allowed_channels"].([]interface{}); ok {
		for _, ch := range chans {
			if s, ok := ch.(string); ok {
				c.allowedChannels[s] = true
			}
		}
	}

	return c, nil
}

// Factory returns a ConnectorFactory for Slack.
func Factory(cfg channels.ConnectorConfig, handler channels.MessageHandler) (channels.Connector, error) {
	return NewConnector(cfg, handler)
}

func (c *Connector) Start(ctx context.Context) error {
	c.status.Store(channels.StatusConnecting)

	// Get bot user ID
	authResp, err := c.client.AuthTest()
	if err != nil {
		c.status.Store(channels.StatusError)
		return fmt.Errorf("slack: auth test failed: %w", err)
	}
	c.botUserID = authResp.UserID
	c.status.Store(channels.StatusConnected)

	logger.WithFields("bot_user_id", c.botUserID, "team", authResp.Team, "channel", c.config.Name).
		Info("slack: connected and authenticated")

	// Process events
	go c.processEvents(ctx)

	err = c.socketMod.RunContext(ctx)
	logger.WithFields("channel", c.config.Name, "error", err).
		Info("slack: RunContext returned")
	return err
}

func (c *Connector) Stop(_ context.Context) error {
	c.status.Store(channels.StatusDisconnected)
	return nil
}

func (c *Connector) Send(_ context.Context, channelRef string, threadRef string, msg channels.OutboundMessage) (string, error) {
	var opts []slack.MsgOption

	if threadRef != "" {
		opts = append(opts, slack.MsgOptionTS(threadRef))
	}

	// Build rich formatting using Slack attachments (supports color bar) with Block Kit blocks inside.
	// When embeds are present, msg.Text is used only as the attachment Fallback (push notifications /
	// accessibility) so Slack doesn't render duplicate content above the attachment.
	if len(msg.Embeds) > 0 {
		var attachments []slack.Attachment
		for _, e := range msg.Embeds {
			var blocks []slack.Block

			if e.Title != "" {
				blocks = append(blocks, slack.NewHeaderBlock(
					slack.NewTextBlockObject("plain_text", e.Title, false, false),
				))
			}
			if e.Description != "" {
				blocks = append(blocks, slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", e.Description, false, false),
					nil, nil,
				))
			}
			if e.CodeBlock != "" {
				blocks = append(blocks, slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", "```\n"+e.CodeBlock+"\n```", false, false),
					nil, nil,
				))
			}

			// Render fields — batch inline fields into section field groups (max 10 per section).
			if len(e.Fields) > 0 {
				var fieldTexts []*slack.TextBlockObject
				for _, f := range e.Fields {
					fieldTexts = append(fieldTexts, slack.NewTextBlockObject("mrkdwn", "*"+f.Name+"*\n"+f.Value, false, false))
					if !f.Inline || len(fieldTexts) >= 10 {
						blocks = append(blocks, slack.NewSectionBlock(nil, fieldTexts, nil))
						fieldTexts = nil
					}
				}
				if len(fieldTexts) > 0 {
					blocks = append(blocks, slack.NewSectionBlock(nil, fieldTexts, nil))
				}
			}

			if len(blocks) > 0 {
				att := slack.Attachment{
					Blocks:   slack.Blocks{BlockSet: blocks},
					Fallback: msg.Text, // shown in push notifications and screen readers
				}
				if e.Color != 0 {
					att.Color = fmt.Sprintf("#%06x", e.Color)
				}
				attachments = append(attachments, att)
			}
		}
		if len(attachments) > 0 {
			opts = append(opts, slack.MsgOptionAttachments(attachments...))
		}
	} else {
		// Plain text message — no embeds.
		opts = append(opts, slack.MsgOptionText(msg.Text, false))
	}

	// Add action buttons (HITL approve/deny)
	if len(msg.Actions) > 0 {
		var elements []slack.BlockElement
		for _, action := range msg.Actions {
			style := slack.StyleDefault
			switch action.Style {
			case "primary":
				style = slack.StylePrimary
			case "danger":
				style = slack.StyleDanger
			}
			btn := slack.NewButtonBlockElement(action.ID, action.ID, slack.NewTextBlockObject("plain_text", action.Label, false, false))
			btn.Style = style
			elements = append(elements, btn)
		}
		actionBlock := slack.NewActionBlock("hitl_actions", elements...)
		opts = append(opts, slack.MsgOptionBlocks(actionBlock))
	}

	_, ts, err := c.client.PostMessage(channelRef, opts...)
	if err != nil {
		return "", err
	}
	return ts, nil
}

func (c *Connector) SendTyping(_ context.Context, _ string) error {
	// Slack doesn't support typing indicators for bots in the same way
	return nil
}

func (c *Connector) EditMessage(_ context.Context, channelRef string, messageRef string, msg channels.OutboundMessage) error {
	_, _, _, err := c.client.UpdateMessage(channelRef, messageRef,
		slack.MsgOptionText(msg.Text, false),
	)
	return err
}

func (c *Connector) Status() channels.ConnectorStatus {
	return c.status.Load().(channels.ConnectorStatus)
}

func (c *Connector) Platform() channels.Platform {
	return channels.PlatformSlack
}

func (c *Connector) processEvents(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.WithFields("channel", c.config.Name, "panic", fmt.Sprintf("%v", r)).
				Error("slack: processEvents goroutine panicked — no more events will be processed")
			c.status.Store(channels.StatusError)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			logger.WithFields("channel", c.config.Name).Debug("slack: processEvents stopping (context cancelled)")
			return
		case evt, ok := <-c.socketMod.Events:
			if !ok {
				logger.WithFields("channel", c.config.Name).Warn("slack: events channel closed")
				return
			}

			logger.WithFields("channel", c.config.Name, "event_type", evt.Type).
				Debug("slack: received event")

			switch evt.Type {
			case socketmode.EventTypeHello:
				logger.WithFields("channel", c.config.Name).
					Info("slack: hello received — socket mode connection confirmed")

			case socketmode.EventTypeConnecting:
				logger.WithFields("channel", c.config.Name).
					Info("slack: socket mode connecting")

			case socketmode.EventTypeConnected:
				logger.WithFields("channel", c.config.Name).
					Info("slack: socket mode connected")

			case socketmode.EventTypeConnectionError:
				logger.WithFields("channel", c.config.Name, "data", fmt.Sprintf("%v", evt.Data)).
					Error("slack: socket mode connection error")

			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					logger.WithFields("channel", c.config.Name).
						Warn("slack: EventTypeEventsAPI with unexpected data type")
					continue
				}
				if evt.Request == nil {
					logger.WithFields("channel", c.config.Name).
						Warn("slack: EventTypeEventsAPI with nil Request, cannot ack")
					continue
				}
				c.socketMod.Ack(*evt.Request)

				switch eventsAPIEvent.Type {
				case slackevents.CallbackEvent:
					logger.WithFields("channel", c.config.Name, "inner_type", eventsAPIEvent.InnerEvent.Type).
						Debug("slack: processing callback event")
					c.handleCallbackEvent(ctx, eventsAPIEvent.InnerEvent)
				}

			case socketmode.EventTypeInteractive:
				callback, ok := evt.Data.(slack.InteractionCallback)
				if !ok {
					logger.WithFields("channel", c.config.Name).
						Warn("slack: EventTypeInteractive with unexpected data type")
					continue
				}
				if evt.Request == nil {
					logger.WithFields("channel", c.config.Name).
						Warn("slack: EventTypeInteractive with nil Request, cannot ack")
					continue
				}
				c.socketMod.Ack(*evt.Request)

				if c.interactionHandler != nil && len(callback.ActionCallback.BlockActions) > 0 {
					for _, action := range callback.ActionCallback.BlockActions {
						logger.WithFields("channel", c.config.Name, "action_id", action.ActionID, "user", callback.User.Name).
							Debug("slack: processing interaction")
						interaction := channels.Interaction{
							Platform:           channels.PlatformSlack,
							ChannelConfigID:    c.config.ID,
							PlatformChannelRef: callback.Channel.ID,
							PlatformUserID:     callback.User.ID,
							PlatformUserName:   callback.User.Name,
							ActionID:           action.ActionID,
							MessageRef:         callback.Message.Timestamp,
						}
						if err := c.interactionHandler(ctx, interaction); err != nil {
							logger.WithError(err).Error("slack: interaction handler failed")
						}
					}
				}

			default:
				logger.WithFields("channel", c.config.Name, "event_type", evt.Type).
					Debug("slack: unhandled event type")
			}
		}
	}
}

// SetInteractionHandler registers a handler for button clicks (HITL approve/deny).
func (c *Connector) SetInteractionHandler(h channels.InteractionHandler) {
	c.interactionHandler = h
}

func (c *Connector) handleCallbackEvent(ctx context.Context, innerEvent slackevents.EventsAPIInnerEvent) {
	switch ev := innerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		c.handleMessage(ctx, ev)
	// app_mention events are intentionally not handled here.
	// When a user @mentions the bot, Slack fires both a message event and an
	// app_mention event for the same message. handleMessage already processes
	// mentions (and strips the <@BOT> prefix), so handling app_mention too
	// would cause the agent to process the same message twice.
	}
}

func (c *Connector) handleMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	// Ignore bot messages
	if ev.BotID != "" || ev.User == c.botUserID || ev.User == "" {
		return
	}

	// Ignore subtypes (edits, deletes, etc.)
	if ev.SubType != "" {
		return
	}

	// Check allowed channels
	if len(c.allowedChannels) > 0 && !c.allowedChannels[ev.Channel] {
		return
	}

	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return
	}

	// Remove bot mention
	text = strings.ReplaceAll(text, "<@"+c.botUserID+">", "")
	text = strings.TrimSpace(text)

	threadRef := ev.ThreadTimeStamp
	if threadRef == "" {
		threadRef = ev.TimeStamp // Use message TS as thread root if no thread
	}

	msg := channels.InboundMessage{
		Platform:           channels.PlatformSlack,
		ChannelConfigID:    c.config.ID,
		AgentID:            c.config.AgentID,
		TenantID:           c.config.TenantID,
		PlatformChannelRef: ev.Channel,
		PlatformUserID:     ev.User,
		PlatformUserName:   ev.User, // Will be resolved below
		PlatformThreadRef:  threadRef,
		Text:               text,
		SessionMode:        c.config.SessionMode,
	}

	// Resolve username
	if userInfo, err := c.client.GetUserInfo(ev.User); err == nil {
		msg.PlatformUserName = userInfo.Profile.DisplayName
		if msg.PlatformUserName == "" {
			msg.PlatformUserName = userInfo.RealName
		}
	}

	if err := c.handler(ctx, msg); err != nil {
		logger.WithFields("channel", c.config.Name, "user", ev.User).WithError(err).
			Error("slack: failed to handle message")
	}
}

// ListPlatformChannels implements channels.ChannelLister.
// Uses conversations.list API (channels:read scope) to list trooper channels.
func (c *Connector) ListPlatformChannels(ctx context.Context) ([]channels.PlatformChannel, error) {
	if c.Status() != channels.StatusConnected {
		return nil, fmt.Errorf("slack: connector not connected")
	}

	var result []channels.PlatformChannel
	cursor := ""

	// Try public + private first; fall back to public-only if groups:read scope is missing
	types := []string{"public_channel", "private_channel"}
	for attempt := 0; attempt < 2; attempt++ {
		cursor = ""
		result = nil

		for {
			params := &slack.GetConversationsParameters{
				Types:           types,
				Limit:           200,
				ExcludeArchived: true,
				Cursor:          cursor,
			}
			convs, nextCursor, err := c.client.GetConversationsContext(ctx, params)
			if err != nil {
				// If missing_scope and we included private channels, retry with public only
				if attempt == 0 && strings.Contains(err.Error(), "missing_scope") {
					logger.WithFields("channel", c.config.Name).
						Debug("slack: groups:read scope not available, falling back to public channels only")
					types = []string{"public_channel"}
					break
				}
				return nil, fmt.Errorf("slack: list conversations: %w", err)
			}

			for _, ch := range convs {
				chType := "public"
				if ch.IsPrivate {
					chType = "private"
				}
				result = append(result, channels.PlatformChannel{
					ID:   ch.ID,
					Name: "#" + ch.Name,
					Type: chType,
				})
			}

			if nextCursor == "" {
				return result, nil
			}
			cursor = nextCursor
		}
	}

	return result, nil
}

// FetchHistory implements channels.HistoryFetcher. It fetches recent messages
// from a Slack thread or channel so the agent can read conversation context.
func (c *Connector) FetchHistory(channelRef, threadRef string, limit int) ([]channels.ContextMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var msgs []slack.Message

	if threadRef != "" {
		replies, _, _, err := c.client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: channelRef,
			Timestamp: threadRef,
			Limit:     limit,
		})
		if err != nil {
			return nil, fmt.Errorf("fetch thread replies: %w", err)
		}
		msgs = replies
	}

	// If no thread or no thread messages, fetch recent channel messages
	if len(msgs) == 0 {
		history, err := c.client.GetConversationHistory(&slack.GetConversationHistoryParameters{
			ChannelID: channelRef,
			Limit:     limit,
		})
		if err != nil {
			return nil, fmt.Errorf("fetch channel history: %w", err)
		}
		msgs = history.Messages
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	// Build context messages (reverse order — oldest first)
	result := make([]channels.ContextMessage, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}

		// Resolve user mention markup
		text = strings.ReplaceAll(text, "<@"+c.botUserID+">", "@Everbot")

		userName := m.Username
		isBot := m.BotID != "" || m.User == c.botUserID
		if !isBot && m.User != "" {
			if userInfo, err := c.client.GetUserInfo(m.User); err == nil {
				userName = userInfo.Profile.DisplayName
				if userName == "" {
					userName = userInfo.RealName
				}
			}
			if userName == "" {
				userName = m.User
			}
		} else if isBot {
			userName = "Everbot"
		}

		result = append(result, channels.ContextMessage{
			UserName:  userName,
			Text:      text,
			Timestamp: m.Timestamp,
			IsBot:     isBot,
		})
	}

	return result, nil
}
