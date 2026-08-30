package runtime_config

import "errors"

var (
	// ErrSectionNotFound is returned when a configuration section is not found
	ErrSectionNotFound = errors.New("configuration section not found")

	// ErrInvalidSection is returned when an invalid section name is provided
	ErrInvalidSection = errors.New("invalid configuration section")

	// ErrInvalidConfig is returned when the configuration data is invalid
	ErrInvalidConfig = errors.New("invalid configuration data")

	// ErrVersionMismatch is returned when there's an optimistic locking conflict
	ErrVersionMismatch = errors.New("version mismatch: configuration was modified by another request")

	// ErrInvalidYAML is returned when the YAML content is malformed
	ErrInvalidYAML = errors.New("invalid YAML content")
)
