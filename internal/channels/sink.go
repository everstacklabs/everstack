package channels

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// PendingInputCallback is called when the agent emits a user_input.requested event.
// The channel manager uses this to track which session is waiting for user input,
// so the next inbound message can be routed as a SubmitUserInput instead of a steer.
type PendingInputCallback func(sessionID, inputID string)

// ChannelSink implements agentrt.EventSink to stream agent responses back to
// a messaging platform. It sends typing indicators, tool status messages, and
// formatted final responses.
type ChannelSink struct {
	connector  Connector
	channelRef string
	threadRef  string
	formatter  *Formatter
	config     ConnectorConfig

	// Tracking for edit-in-place tool status
	lastStatusMsgRef string
	typingCancel     context.CancelFunc

	// Temporary "Thinking..." message ref for platforms without native typing
	// indicators (e.g. Slack). Posted on turn start, edited into the first
	// tool status or response, so the user always sees immediate feedback.
	thinkingMsgRef string

	// Callback to notify the channel manager of pending ask_user inputs.
	onPendingInput PendingInputCallback
}

// NewChannelSink creates a new ChannelSink.
func NewChannelSink(connector Connector, channelRef, threadRef string, config ConnectorConfig) *ChannelSink {
	return &ChannelSink{
		connector:  connector,
		channelRef: channelRef,
		threadRef:  threadRef,
		formatter:  NewFormatter(config.Platform, int(config.MaxResponseLen)),
		config:     config,
	}
}

// SetPendingInputCallback sets the callback invoked when ask_user is triggered.
func (s *ChannelSink) SetPendingInputCallback(cb PendingInputCallback) {
	s.onPendingInput = cb
}

// OnEvent handles agent runtime events and translates them to platform actions.
func (s *ChannelSink) OnEvent(event agentrt.Event) error {
	ctx := context.Background()

	switch event.Type {
	case agentrt.EventTurnStart:
		s.startTyping(ctx)

		// For platforms without native typing indicators (Slack), post a
		// temporary "Thinking..." message so the user gets immediate feedback.
		if !s.hasNativeTyping() {
			ref, err := s.connector.Send(ctx, s.channelRef, s.threadRef, OutboundMessage{
				Text: "_Thinking..._",
			})
			if err == nil {
				s.thinkingMsgRef = ref
			}
		}

	case agentrt.EventToolCallStart:
		// Update the thinking message with tool status (if present),
		// so the user sees what the agent is doing without message spam.
		toolName := event.ToolName
		if toolName == "" {
			if tn, ok := event.Data["tool_name"].(string); ok {
				toolName = tn
			}
		}
		statusText := toolStatusMessage(toolName)
		if statusText != "" {
			if s.thinkingMsgRef != "" {
				// Edit the thinking message in-place
				_ = s.connector.EditMessage(ctx, s.channelRef, s.thinkingMsgRef, OutboundMessage{
					Text: "_" + statusText + "_",
				})
			} else {
				// No thinking message — send a new status message
				ref, err := s.connector.Send(ctx, s.channelRef, s.threadRef, OutboundMessage{
					Text:       statusText,
					ToolStatus: statusText,
				})
				if err == nil {
					s.lastStatusMsgRef = ref
				}
			}
		}

	case agentrt.EventToolCallEnd:
		// Edit standalone status message to show completion (only when
		// not using the thinking message flow).
		if s.lastStatusMsgRef != "" {
			toolName := event.ToolName
			if toolName == "" {
				if tn, ok := event.Data["tool_name"].(string); ok {
					toolName = tn
				}
			}
			_ = s.connector.EditMessage(ctx, s.channelRef, s.lastStatusMsgRef, OutboundMessage{
				Text: toolCompletedMessage(toolName),
			})
			s.lastStatusMsgRef = ""
		}

	case agentrt.EventTurnEnd:
		s.stopTyping()

		// Extract the assistant's response text
		text := ""
		if event.TextDelta != "" {
			text = event.TextDelta
		}
		if t, ok := event.Data["assistant_text"].(string); ok && t != "" {
			text = t
		}
		if t, ok := event.Data["response"].(string); ok && t != "" {
			text = t
		}
		if t, ok := event.Data["assistant_output"].(string); ok && t != "" {
			text = t
		}

		if text == "" {
			// No response text — clean up the thinking message
			s.clearThinkingMessage(ctx)
			return nil
		}

		// Format and send chunked messages
		chunks := s.formatter.FormatResponse(text)
		for i, chunk := range chunks {
			if i == 0 && s.thinkingMsgRef != "" {
				// Edit the thinking message into the first response chunk
				_ = s.connector.EditMessage(ctx, s.channelRef, s.thinkingMsgRef, OutboundMessage{
					Text:   chunk,
					Format: s.config.ResponseFormat,
				})
				s.thinkingMsgRef = ""
			} else {
				_, err := s.connector.Send(ctx, s.channelRef, s.threadRef, OutboundMessage{
					Text:   chunk,
					Format: s.config.ResponseFormat,
				})
				if err != nil {
					logger.WithError(err).Warn("channels: failed to send response chunk")
				}
			}
		}

	case agentrt.EventUserInputRequested:
		// Agent called ask_user — send the question to the platform and
		// register the pending input so the next inbound message is routed
		// as a SubmitUserInput instead of a steer.
		s.stopTyping()
		s.clearThinkingMessage(ctx)

		question, _ := event.Data["question"].(string)
		inputID, _ := event.Data["input_id"].(string)
		sessionID := event.SessionID

		if question == "" {
			question = "The agent is waiting for your response."
		}

		// Build message with optional choices
		msgText := question
		if opts, ok := event.Data["options"].([]interface{}); ok && len(opts) > 0 {
			msgText += "\n\nOptions:"
			for i, o := range opts {
				switch v := o.(type) {
				case string:
					msgText += fmt.Sprintf("\n%d. %s", i+1, v)
				case map[string]interface{}:
					label, _ := v["label"].(string)
					description, _ := v["description"].(string)
					if label != "" {
						msgText += fmt.Sprintf("\n%d. %s", i+1, label)
						if description != "" {
							msgText += fmt.Sprintf(" - %s", description)
						}
					}
				}
			}
		}

		_, err := s.connector.Send(ctx, s.channelRef, s.threadRef, OutboundMessage{
			Text: msgText,
		})
		if err != nil {
			logger.WithError(err).Warn("channels: failed to send ask_user question")
		}

		// Notify channel manager to intercept next message as user input
		if s.onPendingInput != nil && inputID != "" && sessionID != "" {
			s.onPendingInput(sessionID, inputID)
		}

	case agentrt.EventApprovalRequested:
		// Send HITL approval message with approve/deny buttons
		toolName := event.ToolName
		toolArgs := event.ToolArgs
		reviewID := event.ReviewID

		description := "Tool **" + toolName + "** requires approval."
		if toolArgs != "" {
			// Truncate long args
			if len(toolArgs) > 500 {
				toolArgs = toolArgs[:500] + "..."
			}
			description += "\n```\n" + toolArgs + "\n```"
		}

		_, err := s.connector.Send(ctx, s.channelRef, s.threadRef, OutboundMessage{
			Text: description,
			Actions: []ActionButton{
				{ID: "approve:" + reviewID, Label: "Approve", Style: "primary"},
				{ID: "deny:" + reviewID, Label: "Deny", Style: "danger"},
			},
		})
		if err != nil {
			logger.WithError(err).Warn("channels: failed to send approval request")
		}

	case agentrt.EventSessionEnd:
		s.stopTyping()
		s.clearThinkingMessage(ctx)
	}

	return nil
}

func (s *ChannelSink) startTyping(ctx context.Context) {
	s.stopTyping()

	typingCtx, cancel := context.WithCancel(ctx)
	s.typingCancel = cancel

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		// Send initial typing indicator
		_ = s.connector.SendTyping(typingCtx, s.channelRef)

		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				_ = s.connector.SendTyping(typingCtx, s.channelRef)
			}
		}
	}()
}

func (s *ChannelSink) stopTyping() {
	if s.typingCancel != nil {
		s.typingCancel()
		s.typingCancel = nil
	}
}

// hasNativeTyping returns true if the platform supports native typing indicators.
func (s *ChannelSink) hasNativeTyping() bool {
	switch s.config.Platform {
	case PlatformDiscord, PlatformTelegram:
		return true
	default:
		return false
	}
}

// clearThinkingMessage removes the temporary thinking message by editing it
// to an empty-ish string (platforms don't support message deletion via edit).
func (s *ChannelSink) clearThinkingMessage(ctx context.Context) {
	if s.thinkingMsgRef != "" {
		// Edit to a minimal placeholder since we can't delete messages
		_ = s.connector.EditMessage(ctx, s.channelRef, s.thinkingMsgRef, OutboundMessage{
			Text: "_Done._",
		})
		s.thinkingMsgRef = ""
	}
}

func toolStatusMessage(toolName string) string {
	switch {
	case strings.HasPrefix(toolName, "sandbox_shell"):
		return "Running code..."
	case strings.HasPrefix(toolName, "sandbox_git"):
		return "Working with git..."
	case strings.HasPrefix(toolName, "sandbox_spawn"):
		return "Spawning environment..."
	case strings.HasPrefix(toolName, "sandbox_expose"):
		return "Exposing port..."
	case toolName == "web_search":
		return "Searching the web..."
	case toolName == "web_fetch":
		return "Fetching web page..."
	case toolName == "memory_store":
		return "Saving to memory..."
	case toolName == "memory_query":
		return "Searching memory..."
	case toolName == "spawn_agent":
		return "Spawning sub-agent..."
	case toolName == "ask_user":
		return "Waiting for input..."
	default:
		// Don't send status messages for unknown/generic tool calls
		// to avoid spamming the chat with "Running X..." / "Done." noise.
		return ""
	}
}

func toolCompletedMessage(toolName string) string {
	switch {
	case strings.HasPrefix(toolName, "sandbox_shell"):
		return "Code executed."
	case toolName == "spawn_agent":
		return "Sub-agent completed."
	default:
		return "Done."
	}
}
