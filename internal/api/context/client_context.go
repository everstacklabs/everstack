package context

import (
	"context"
)

// ClientContext represents client-specific context data
type ClientContext struct {
	ClientID   string
	ClientName string
	APIKey     string
	Origin     string
	UserAgent  string
}

type clientContextKey struct{}

// WithClientContext adds client context to the parent context
func WithClientContext(parent context.Context, clientCtx *ClientContext) context.Context {
	return context.WithValue(parent, clientContextKey{}, clientCtx)
}

// GetClientContext retrieves client context from the context
func GetClientContext(ctx context.Context) *ClientContext {
	if val := ctx.Value(clientContextKey{}); val != nil {
		if clientCtx, ok := val.(*ClientContext); ok {
			return clientCtx
		}
	}
	return &ClientContext{}
}

// SetClientID adds client ID to the context
func SetClientID(ctx context.Context, clientID string) context.Context {
	clientCtx := GetClientContext(ctx)
	clientCtx.ClientID = clientID
	return WithClientContext(ctx, clientCtx)
}

// SetClientName adds client name to the context
func SetClientName(ctx context.Context, clientName string) context.Context {
	clientCtx := GetClientContext(ctx)
	clientCtx.ClientName = clientName
	return WithClientContext(ctx, clientCtx)
}

// SetAPIKey adds API key to the context
func SetAPIKey(ctx context.Context, apiKey string) context.Context {
	clientCtx := GetClientContext(ctx)
	clientCtx.APIKey = apiKey
	return WithClientContext(ctx, clientCtx)
}

// SetOrigin adds origin to the context
func SetOrigin(ctx context.Context, origin string) context.Context {
	clientCtx := GetClientContext(ctx)
	clientCtx.Origin = origin
	return WithClientContext(ctx, clientCtx)
}

// SetUserAgent adds user agent to the context
func SetUserAgent(ctx context.Context, userAgent string) context.Context {
	clientCtx := GetClientContext(ctx)
	clientCtx.UserAgent = userAgent
	return WithClientContext(ctx, clientCtx)
}

// SetAllClientInfo sets all client information at once
func SetAllClientInfo(ctx context.Context, clientID, clientName, apiKey, origin, userAgent string) context.Context {
	clientCtx := &ClientContext{
		ClientID:   clientID,
		ClientName: clientName,
		APIKey:     apiKey,
		Origin:     origin,
		UserAgent:  userAgent,
	}
	return WithClientContext(ctx, clientCtx)
}
