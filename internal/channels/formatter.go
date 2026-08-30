package channels

import (
	"strings"
)

// Platform message length limits.
const (
	DiscordMaxLength  = 2000
	SlackMaxLength    = 4000
	TelegramMaxLength = 4096
)

// Formatter handles response formatting and chunking for platform limits.
type Formatter struct {
	platform  Platform
	maxLength int
}

// NewFormatter creates a Formatter for the given platform.
func NewFormatter(platform Platform, maxResponseLen int) *Formatter {
	maxLen := maxResponseLen
	if maxLen <= 0 {
		switch platform {
		case PlatformDiscord:
			maxLen = DiscordMaxLength
		case PlatformSlack:
			maxLen = SlackMaxLength
		case PlatformTelegram:
			maxLen = TelegramMaxLength
		default:
			maxLen = DiscordMaxLength
		}
	}

	// Clamp to platform max
	switch platform {
	case PlatformDiscord:
		if maxLen > DiscordMaxLength {
			maxLen = DiscordMaxLength
		}
	case PlatformSlack:
		if maxLen > SlackMaxLength {
			maxLen = SlackMaxLength
		}
	case PlatformTelegram:
		if maxLen > TelegramMaxLength {
			maxLen = TelegramMaxLength
		}
	}

	return &Formatter{
		platform:  platform,
		maxLength: maxLen,
	}
}

// FormatResponse splits a response into platform-appropriate chunks.
// It tries to split at paragraph boundaries, then sentence boundaries,
// then word boundaries.
func (f *Formatter) FormatResponse(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if len(text) <= f.maxLength {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= f.maxLength {
			chunks = append(chunks, remaining)
			break
		}

		// Find best split point within maxLength
		chunk := remaining[:f.maxLength]
		splitIdx := f.findSplitPoint(chunk)

		chunks = append(chunks, strings.TrimSpace(remaining[:splitIdx]))
		remaining = strings.TrimSpace(remaining[splitIdx:])
	}

	return chunks
}

// findSplitPoint finds the best position to split a chunk.
func (f *Formatter) findSplitPoint(chunk string) int {
	maxLen := len(chunk)

	// Try to split at double newline (paragraph boundary)
	if idx := strings.LastIndex(chunk, "\n\n"); idx > maxLen/2 {
		return idx + 2
	}

	// Try to split at single newline
	if idx := strings.LastIndex(chunk, "\n"); idx > maxLen/2 {
		return idx + 1
	}

	// Try to split at sentence end
	for _, sep := range []string{". ", "! ", "? "} {
		if idx := strings.LastIndex(chunk, sep); idx > maxLen/2 {
			return idx + len(sep)
		}
	}

	// Try to split at word boundary
	if idx := strings.LastIndex(chunk, " "); idx > maxLen/2 {
		return idx + 1
	}

	// Hard split at max length
	return maxLen
}

// TruncateWithEllipsis truncates text and adds ellipsis if needed.
func TruncateWithEllipsis(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}
