package oauthserver

import (
	"time"

	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
)

// NewDeviceTokenIssuer adapts the shared CLI token manager to an OAuth issuer.
func NewDeviceTokenIssuer(tokens *deviceauth.TokenManager, ttl time.Duration) IssueAccessToken {
	return func(identity Identity, clientID string) (AccessToken, error) {
		token, err := tokens.IssueWithTTL(deviceauth.Identity{
			UserID:           identity.UserID,
			Email:            identity.Email,
			OrganizationID:   identity.OrganizationID,
			OrganizationSlug: identity.OrganizationSlug,
			InstanceID:       identity.InstanceID,
			ClientID:         clientID,
		}, ttl)
		if err != nil {
			return AccessToken{}, err
		}
		return AccessToken{
			Token:     token,
			ExpiresAt: time.Now().UTC().Add(ttl),
		}, nil
	}
}
