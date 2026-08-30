package oidc

import "github.com/golang-jwt/jwt/v5"

// IDClaims are the OIDC ID-token claims the cloud OP issues to an instance RP.
//
// Identity only: there are deliberately NO roles or permissions here.
// Authorization is a separate ReBAC query (pkg/authz) performed at the instance.
// The ID token answers "who is this and which org/instance is this token for".
type IDClaims struct {
	jwt.RegisteredClaims        // iss, sub, aud (=client_id), exp, iat, jti
	Email                string `json:"email,omitempty"`
	EmailVerified        bool   `json:"email_verified,omitempty"`
	Name                 string `json:"name,omitempty"`
	OrgID                string `json:"org_id,omitempty"`
	OrgSlug              string `json:"org_slug,omitempty"`
	InstanceID           string `json:"instance_id,omitempty"`
	Nonce                string `json:"nonce,omitempty"`
	AuthTime             int64  `json:"auth_time,omitempty"`
}

// AccessClaims are the minimal access-token claims for instance->cloud API calls.
type AccessClaims struct {
	jwt.RegisteredClaims
	Scope string `json:"scope,omitempty"`
	OrgID string `json:"org_id,omitempty"`
}
