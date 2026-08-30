package provider_config

import "time"

// ModelStatus represents the status of a model in the catalog
type ModelStatus struct {
	ID           string     `db:"id"`
	ProviderName string     `db:"provider_name"`
	ModelName    string     `db:"model_name"`
	Status       string     `db:"status"`    // "available", "configured", "active", "deprecated"
	Freshness    string     `db:"freshness"` // "new", "stable"
	MarkedNewAt  *time.Time `db:"marked_new_at"`
	DeprecatedAt *time.Time `db:"deprecated_at"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}
