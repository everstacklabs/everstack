package api_key

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// CreateApiKeyCommand requests creation of an API key record.
type CreateApiKeyCommand struct {
	commands.BaseCommand
	Name       string `json:"name"`
	Type       string `json:"type"`
	Plaintext  string `json:"plaintext"`
	OrgID      string `json:"org_id"`
	InstanceID string `json:"instance_id"`
}

func NewCreateApiKeyCommand(name, keyType, plaintext, userID, orgID, instanceID string) *CreateApiKeyCommand {
	return &CreateApiKeyCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
		},
		Name:       name,
		Type:       keyType,
		Plaintext:  plaintext,
		OrgID:      orgID,
		InstanceID: instanceID,
	}
}

func (c CreateApiKeyCommand) AggregateID() string { return c.ID }
func (c CreateApiKeyCommand) CommandType() string { return "CreateApiKey" }
func (c CreateApiKeyCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if c.Type == "" {
		return fmt.Errorf("type cannot be empty")
	}
	if c.Plaintext == "" {
		return fmt.Errorf("plaintext cannot be empty")
	}
	return nil
}

// RevokeApiKeyCommand requests revocation of an API key record by id, scoped
// to the caller's org. The OrgID is mandatory because the projection's UPDATE
// filters on it — without that filter, a caller who guessed any key id could
// revoke another tenant's key.
type RevokeApiKeyCommand struct {
	commands.BaseCommand
	KeyID string `json:"key_id"`
	OrgID string `json:"org_id"`
}

func NewRevokeApiKeyCommand(keyID, orgID, userID, traceID string) *RevokeApiKeyCommand {
	return &RevokeApiKeyCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		KeyID: keyID,
		OrgID: orgID,
	}
}

func (c RevokeApiKeyCommand) AggregateID() string { return c.KeyID }
func (c RevokeApiKeyCommand) CommandType() string { return "RevokeApiKey" }
func (c RevokeApiKeyCommand) Validate() error {
	if c.KeyID == "" {
		return fmt.Errorf("key_id cannot be empty")
	}
	if c.OrgID == "" {
		return fmt.Errorf("org_id cannot be empty")
	}
	return nil
}
