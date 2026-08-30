package selfhosted

import "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"

// Re-export config types so callers of the selfhosted package keep working.
type SessionConfig = domain.SessionConfig
type InternalConfig = domain.InternalConfig
