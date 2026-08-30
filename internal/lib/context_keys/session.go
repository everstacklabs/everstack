package contextkeys

import (
	"context"
	"strings"

	"github.com/everstacklabs/everstack/internal/api/common"
	"google.golang.org/grpc/metadata"
)

// SessionIDKey stores a caller-supplied observability session ID in context.
// ThreadIDKey stores a caller-supplied conversational thread ID.
const (
	SessionIDKey key = iota + 200
	ThreadIDKey
)

// Session ID header names (in order of priority). These let a caller group
// related traces under one session across any module (agent, workflow,
// sandbox, eval, voice, function), the same way Braintrust/Langfuse allow a
// caller-supplied session id.
var sessionIDHeaders = []string{
	common.XSessionID,
	common.EverstackSessionID, // canonical x-evs-session-id
	common.LegacyMFSessionID,  // legacy x-mf-session-id (backward compat)
}

// ExtractSessionID returns a caller-supplied session ID from context or
// headers, or "" if none was provided. Unlike ExtractUserID there is no
// generated fallback: callers (and module roots) decide the default grouping
// when no explicit session is supplied.
func ExtractSessionID(ctx context.Context) string {
	// 1. Already stored in context.
	if sessionID, ok := ctx.Value(SessionIDKey).(string); ok && sessionID != "" {
		return sessionID
	}

	// 2. gRPC metadata headers.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, header := range sessionIDHeaders {
			if values := md.Get(header); len(values) > 0 && values[0] != "" {
				return strings.TrimSpace(values[0])
			}
		}
	}

	return ""
}

// WithSessionID returns a new context with the session ID stored.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, SessionIDKey, sessionID)
}

// GetSessionID retrieves a stored session ID from context, or "".
func GetSessionID(ctx context.Context) string {
	if sessionID, ok := ctx.Value(SessionIDKey).(string); ok {
		return sessionID
	}
	return ""
}

// Thread ID header names (in order of priority). A thread is a conversational
// continuation distinct from a session, so callers can group multi-turn flows
// independently of session boundaries.
var threadIDHeaders = []string{
	common.XThreadID,
	common.EverstackThreadID, // canonical x-evs-thread-id
	common.LegacyMFThreadID,  // legacy x-mf-thread-id (backward compat)
}

// ExtractThreadID returns a caller-supplied thread ID from context or headers,
// or "" if none was provided.
func ExtractThreadID(ctx context.Context) string {
	if threadID, ok := ctx.Value(ThreadIDKey).(string); ok && threadID != "" {
		return threadID
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, header := range threadIDHeaders {
			if values := md.Get(header); len(values) > 0 && values[0] != "" {
				return strings.TrimSpace(values[0])
			}
		}
	}
	return ""
}

// WithThreadID returns a new context with the thread ID stored.
func WithThreadID(ctx context.Context, threadID string) context.Context {
	if threadID == "" {
		return ctx
	}
	return context.WithValue(ctx, ThreadIDKey, threadID)
}
