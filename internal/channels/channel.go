// Package channels provides messaging platform integration for the agent runtime.
// Connectors translate platform messages into agent session turns and stream
// responses back via EventSink.
package channels

import (
	"context"
	"time"
)

// Platform identifies a messaging platform.
type Platform string

const (
	PlatformDiscord  Platform = "discord"
	PlatformSlack    Platform = "slack"
	PlatformTelegram Platform = "telegram"
)

// SessionMode controls how platform messages map to agent sessions.
type SessionMode string

const (
	SessionModeShared  SessionMode = "shared"
	SessionModePerUser SessionMode = "per_user"
	SessionModeThread  SessionMode = "thread"
)

// ConnectorStatus represents the runtime connection status of a connector.
type ConnectorStatus string

const (
	StatusConnected    ConnectorStatus = "connected"
	StatusDisconnected ConnectorStatus = "disconnected"
	StatusConnecting   ConnectorStatus = "connecting"
	StatusError        ConnectorStatus = "error"
)

// InboundMessage represents a message received from a messaging platform.
type InboundMessage struct {
	Platform        Platform
	ChannelConfigID string
	AgentID         string
	TrooperID     string // Set when routed to a trooper instead of an agent
	TenantID        string

	// Platform identifiers
	PlatformChannelRef string // Discord channel ID, Slack channel ID, etc.
	PlatformUserID     string
	PlatformUserName   string
	PlatformThreadRef  string // Thread ID if applicable

	// Content
	Text        string
	Attachments []Attachment
	Timestamp   time.Time

	// Session resolution
	SessionMode SessionMode

	// Web search intent (channel policy may override)
	WebSearchRequested bool
}

// ContextMessage represents a recent message from the channel/thread,
// used to give the agent conversational context.
type ContextMessage struct {
	UserName  string
	Text      string
	Timestamp string
	IsBot     bool
}

// Attachment represents a file or media attachment.
type Attachment struct {
	URL      string
	Filename string
	MimeType string
	Size     int64
}

// OutboundMessage represents a message to send to a messaging platform.
type OutboundMessage struct {
	Text       string
	IsPartial  bool   // True for streaming/typing updates
	IsEdit     bool   // True to edit a previous message
	EditRef    string // Platform message ID to edit
	Format     string // plain, rich, auto
	Embeds     []Embed
	ToolStatus string         // e.g., "Running code..."
	Actions    []ActionButton // Interactive buttons (e.g., HITL approve/deny)
}

// ActionButton represents an interactive button on a message (e.g., HITL approval).
type ActionButton struct {
	ID    string // Unique action identifier (e.g., "approve:session_id:tool_call_id")
	Label string // Button text
	Style string // primary, danger, secondary
}

// Embed represents a rich embed (Discord) or block (Slack).
type Embed struct {
	Title       string
	Description string
	Color       int
	Fields      []EmbedField
	CodeBlock   string
}

// EmbedField is a key-value pair in an embed.
type EmbedField struct {
	Name   string
	Value  string
	Inline bool
}

// Connector is the interface for a messaging platform connection.
// Each platform (Discord, Slack, Telegram) implements this.
type Connector interface {
	// Start connects to the platform and begins listening for messages.
	Start(ctx context.Context) error

	// Stop gracefully disconnects from the platform.
	Stop(ctx context.Context) error

	// Send sends a message to a specific platform channel/thread.
	Send(ctx context.Context, channelRef string, threadRef string, msg OutboundMessage) (messageRef string, err error)

	// SendTyping sends a typing indicator to a channel.
	SendTyping(ctx context.Context, channelRef string) error

	// EditMessage edits a previously sent message.
	EditMessage(ctx context.Context, channelRef string, messageRef string, msg OutboundMessage) error

	// Status returns the current connection status.
	Status() ConnectorStatus

	// Platform returns the platform type.
	Platform() Platform
}

// HistoryFetcher is an optional interface that connectors can implement to
// allow the agent to read channel/thread history on demand.
type HistoryFetcher interface {
	// FetchHistory returns recent messages from a channel or thread.
	// If threadRef is non-empty, fetches thread replies; otherwise channel messages.
	// limit controls how many messages to return (capped by implementation).
	FetchHistory(channelRef, threadRef string, limit int) ([]ContextMessage, error)
}

// PlatformChannel represents a channel/room from the connected platform.
type PlatformChannel struct {
	ID   string
	Name string
	Type string // "text", "public", "private"
}

// ChannelLister is an optional interface that connectors can implement to
// list available channels/rooms from the connected platform.
type ChannelLister interface {
	ListPlatformChannels(ctx context.Context) ([]PlatformChannel, error)
}

// MessageHandler is called by connectors when they receive a message.
type MessageHandler func(ctx context.Context, msg InboundMessage) error

// InteractionHandler is called when a user clicks an action button (e.g., HITL approve/deny).
type InteractionHandler func(ctx context.Context, interaction Interaction) error

// Interaction represents a user clicking a button or selecting a menu item.
type Interaction struct {
	Platform           Platform
	ChannelConfigID    string
	PlatformChannelRef string
	PlatformUserID     string
	PlatformUserName   string
	ActionID           string // The ActionButton.ID that was clicked
	MessageRef         string // The message the button was on
}

// ConnectorConfig holds the config needed to create a connector.
// AgentID may be empty when the channel operates in dispatcher mode —
// messages are routed dynamically via mention matching or HITL confirmation.
type ConnectorConfig struct {
	ID              string
	TenantID        string
	AgentID         string // empty = dispatcher mode (no default agent)
	Platform        Platform
	Name            string
	SessionMode     SessionMode
	Credentials     map[string]interface{}
	PlatformConfig  map[string]interface{}
	MaxMsgPerMinute int32
	MaxSessions     int32
	ResponseFormat  string
	MaxResponseLen  int32
	MaxTokensPerDay int64
	IdleSessionTTL  time.Duration
	CoalesceWindow  time.Duration
}
