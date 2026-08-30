package license

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/crypto"
	"github.com/everstacklabs/everstack/internal/query"
)

const GetInstanceCredentialsType = "license.get_instance_credentials"

type GetInstanceCredentials struct {
	query.BaseQuery
}

func (q GetInstanceCredentials) QueryType() string { return GetInstanceCredentialsType }
func (q GetInstanceCredentials) Validate() error   { return nil }

type InstanceCredentials struct {
	InstanceID    string
	RefreshToken  string
	SigningKey     []byte // M2M signing key for JWT authentication
	SignedPayload []byte // Raw JSON from system.instances.signed_payload (contains license_jwt)
}

type GetInstanceCredentialsHandler struct{ db *sqlx.DB }

func NewGetInstanceCredentialsHandler(db *sqlx.DB) *GetInstanceCredentialsHandler {
	return &GetInstanceCredentialsHandler{db: db}
}

func (h *GetInstanceCredentialsHandler) QueryType() string { return GetInstanceCredentialsType }

func (h *GetInstanceCredentialsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	if h.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}

	var instanceID, refreshToken, encryptedKeyStr, localInstanceID sql.NullString
	var legacySigningKey sql.NullString
	var signedPayload sql.NullString

	// Read instance credentials and both signing key formats:
	// - m2m_signing_key_encrypted: New encrypted M2M JWT signing key (preferred)
	// - signing_key: Legacy HMAC signing key from activation (fallback)
	// - local_instance_id: Needed for decrypting the new signing key
	// - signed_payload: JSONB containing license_jwt for local JWT verification
	err := h.db.QueryRowContext(ctx, `
		SELECT instance_kid, instance_signature, m2m_signing_key_encrypted, signing_key, local_instance_id, signed_payload
		FROM system.instances
		WHERE instance_status='active'
		ORDER BY updated_at DESC
		LIMIT 1
	`).Scan(&instanceID, &refreshToken, &encryptedKeyStr, &legacySigningKey, &localInstanceID, &signedPayload)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if !instanceID.Valid || !refreshToken.Valid {
		return nil, nil
	}

	creds := &InstanceCredentials{
		InstanceID:   instanceID.String,
		RefreshToken: refreshToken.String,
	}

	// Include signed_payload if present
	if signedPayload.Valid && signedPayload.String != "" && signedPayload.String != "{}" {
		creds.SignedPayload = []byte(signedPayload.String)
	}

	// Priority 1: Try to decrypt the new M2M signing key
	if encryptedKeyStr.Valid && encryptedKeyStr.String != "" && localInstanceID.Valid && localInstanceID.String != "" {
		// Derive encryption key from local_instance_id
		const encryptionSalt = "everstack-m2m-signing-key-encryption-v1"
		derivedKey, err := crypto.DeriveKey(localInstanceID.String, encryptionSalt)
		if err == nil {
			// Decrypt the signing key
			signingKey, err := crypto.Decrypt(encryptedKeyStr.String, derivedKey)
			if err == nil && len(signingKey) >= 32 {
				creds.SigningKey = signingKey
				return creds, nil
			}
		}
	}

	// Priority 2: Fall back to legacy signing key (base64-encoded, unencrypted)
	if legacySigningKey.Valid && legacySigningKey.String != "" {
		signingKey, err := base64.StdEncoding.DecodeString(legacySigningKey.String)
		if err == nil {
			creds.SigningKey = signingKey
		}
	}

	return creds, nil
}
