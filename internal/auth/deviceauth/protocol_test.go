package deviceauth

import (
	"errors"
	"testing"
	"time"
)

func TestResultForErrorPreservesLegacyStatusAndAddsOAuthError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err        error
		status     string
		oauthError string
	}{
		{err: ErrAuthorizationPending, status: "pending", oauthError: "authorization_pending"},
		{err: ErrSlowDown, status: "pending", oauthError: "slow_down"},
		{err: ErrAuthorizationDenied, status: "denied", oauthError: "access_denied"},
		{err: ErrSessionExpired, status: "expired", oauthError: "expired_token"},
		{err: ErrSessionConsumed, status: "expired", oauthError: "expired_token"},
	}
	for _, test := range tests {
		result, ok := ResultForError(test.err)
		if !ok || result.Status != test.status || result.OAuthError != test.oauthError {
			t.Errorf("ResultForError(%v) = %#v, %v; want status=%q oauth_error=%q", test.err, result, ok, test.status, test.oauthError)
		}
	}
	if result, ok := ResultForError(errors.New("database unavailable")); ok || result != (ProtocolResult{}) {
		t.Fatalf("ResultForError(unrelated error) = %#v, %v; want empty, false", result, ok)
	}
}

func TestBrowserStatusHidesConsumedState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	status, valid, expired := BrowserStatus(&Session{
		Status:    StatusConsumed,
		ExpiresAt: now.Add(time.Minute),
	}, now)
	if status != "expired" || valid || !expired {
		t.Fatalf("BrowserStatus(consumed) = %q, %v, %v; want expired, false, true", status, valid, expired)
	}
}
