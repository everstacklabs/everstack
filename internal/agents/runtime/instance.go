package runtime

import (
	"os"

	"github.com/google/uuid"
)

// InstanceID returns a stable identifier for this server instance.
// It uses the hostname when available, falling back to a random UUID.
func InstanceID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return uuid.New().String()
}
