package telemetry

import (
	"encoding/json"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// MessageMetadata contains analyzed metadata from request messages
type MessageMetadata struct {
	Roles                 []string   // Array of role strings (e.g., ["system", "user", "assistant"])
	Sizes                 []int      // Size in bytes of each message content
	HasSystemPrompt       bool       // Whether the message list contains a system message
	HasImages             bool       // Whether any message contains image content
	HasToolCalls          bool       // Whether any message contains tool calls
	ContentTypes          [][]string // Content types per message (e.g., [["text"], ["text", "image_url"]])
	TotalChars            int        // Total character count across all messages
	SystemPromptLength    int        // Character length of system prompt (if present)
	UserMessageCount      int        // Number of user messages
	AssistantMessageCount int        // Number of assistant messages
	ToolMessageCount      int        // Number of tool messages
	FunctionMessageCount  int        // Number of function messages
}

// AnalyzeMessages extracts metadata from a list of messages for span attributes
func AnalyzeMessages(messages []gw.Message) MessageMetadata {
	meta := MessageMetadata{
		Roles:        make([]string, 0, len(messages)),
		Sizes:        make([]int, 0, len(messages)),
		ContentTypes: make([][]string, 0, len(messages)),
	}

	for _, msg := range messages {
		// Track role
		roleStr := string(msg.Role)
		meta.Roles = append(meta.Roles, roleStr)

		// Count by role
		switch msg.Role {
		case gw.RoleSystem:
			meta.HasSystemPrompt = true
		case gw.RoleUser:
			meta.UserMessageCount++
		case gw.RoleAssistant:
			meta.AssistantMessageCount++
		case gw.RoleTool:
			meta.ToolMessageCount++
		case gw.RoleFunction:
			meta.FunctionMessageCount++
		}

		// Analyze content parts
		var messageSize int
		var contentTypes []string
		for _, part := range msg.Content {
			contentTypes = append(contentTypes, part.Type)

			// Check for images
			if part.Type == "image_url" {
				meta.HasImages = true
			}

			// Check for tool calls
			if part.ToolCallID != nil {
				meta.HasToolCalls = true
			}

			// Calculate size
			if part.Text != nil {
				textLen := len(*part.Text)
				messageSize += textLen
				meta.TotalChars += textLen

				// Track system prompt length
				if msg.Role == gw.RoleSystem {
					meta.SystemPromptLength += textLen
				}
			}
		}

		meta.Sizes = append(meta.Sizes, messageSize)
		meta.ContentTypes = append(meta.ContentTypes, contentTypes)
	}

	return meta
}

// ResponseMetadata contains analyzed metadata from a response
type ResponseMetadata struct {
	ChoiceCount            int      // Number of choices in the response
	TotalContentLength     int      // Total character count across all choices
	FinishReasons          []string // Finish reason for each choice
	HasMultipleChoices     bool     // Whether there are multiple choices
	ContentLengthPerChoice []int    // Character count per choice
}

// AnalyzeResponse extracts metadata from a chat completion response
func AnalyzeResponse(resp gw.ChatCompletionResponse) ResponseMetadata {
	meta := ResponseMetadata{
		ChoiceCount:            len(resp.Choices),
		HasMultipleChoices:     len(resp.Choices) > 1,
		FinishReasons:          make([]string, 0, len(resp.Choices)),
		ContentLengthPerChoice: make([]int, 0, len(resp.Choices)),
	}

	for _, choice := range resp.Choices {
		// Track finish reason
		if choice.FinishReason != "" {
			meta.FinishReasons = append(meta.FinishReasons, choice.FinishReason)
		}

		// Calculate content length for this choice
		var choiceLength int
		for _, part := range choice.Message.Content {
			if part.Text != nil {
				choiceLength += len(*part.Text)
			}
		}
		meta.ContentLengthPerChoice = append(meta.ContentLengthPerChoice, choiceLength)
		meta.TotalContentLength += choiceLength
	}

	return meta
}

// SerializeRoles converts role array to JSON string
func SerializeRoles(roles []string) string {
	if len(roles) == 0 {
		return "[]"
	}
	data, err := json.Marshal(roles)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SerializeSizes converts size array to JSON string
func SerializeSizes(sizes []int) string {
	if len(sizes) == 0 {
		return "[]"
	}
	data, err := json.Marshal(sizes)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SerializeContentTypes converts content types array to JSON string
func SerializeContentTypes(contentTypes [][]string) string {
	if len(contentTypes) == 0 {
		return "[]"
	}
	data, err := json.Marshal(contentTypes)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SerializeFinishReasons converts finish reasons array to JSON string
func SerializeFinishReasons(finishReasons []string) string {
	if len(finishReasons) == 0 {
		return "[]"
	}
	data, err := json.Marshal(finishReasons)
	if err != nil {
		return "[]"
	}
	return string(data)
}
