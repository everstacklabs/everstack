package deviceauth

import (
	"errors"
	"time"
)

type ProtocolResult struct {
	Status     string
	OAuthError string
}

// ResultForError preserves the existing status contract while also exposing
// the precise RFC 8628 token error to clients that understand it.
func ResultForError(err error) (ProtocolResult, bool) {
	switch {
	case errors.Is(err, ErrAuthorizationPending):
		return ProtocolResult{Status: "pending", OAuthError: "authorization_pending"}, true
	case errors.Is(err, ErrSlowDown):
		return ProtocolResult{Status: "pending", OAuthError: "slow_down"}, true
	case errors.Is(err, ErrAuthorizationDenied):
		return ProtocolResult{Status: "denied", OAuthError: "access_denied"}, true
	case errors.Is(err, ErrSessionExpired), errors.Is(err, ErrSessionConsumed):
		return ProtocolResult{Status: "expired", OAuthError: "expired_token"}, true
	default:
		return ProtocolResult{}, false
	}
}

// BrowserStatus keeps internal terminal states out of the approval-page
// contract. A consumed session is no longer valid and is presented the same as
// an expired session.
func BrowserStatus(session *Session, now time.Time) (status string, valid, expired bool) {
	expired = !now.UTC().Before(session.ExpiresAt) || session.Status == StatusConsumed
	if expired {
		return "expired", false, true
	}
	return string(session.Status), true, false
}
