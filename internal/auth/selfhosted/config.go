package selfhosted

import (
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
)

// Re-export config types so callers of the selfhosted package keep working.
type SessionConfig = domain.SessionConfig
type InternalConfig = domain.InternalConfig

// EmailSender is re-exported so cmd/ can declare the injection slot without
// reaching into the service subpackage.
type EmailSender = service.EmailSender
