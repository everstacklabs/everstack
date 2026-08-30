// Package events provides event bus and visibility types for the CQRS event system.
package events

// EventVisibility defines who can see an event in audit logs and where it should be stored.
type EventVisibility string

const (
	// VisibilityUser indicates events visible in user-facing audit logs.
	// These are stored in the user's local database (Postgres/ClickHouse).
	// Examples: configuration changes, API key operations, provider config.
	VisibilityUser EventVisibility = "user"

	// VisibilityInternal indicates events for Everstack Cloud/License Service only.
	// These are dispatched to the License Service and stored in internal infrastructure.
	// NEVER stored in the user's local database to prevent manipulation.
	// Examples: detailed usage telemetry, session data, token counts, billing metrics.
	VisibilityInternal EventVisibility = "internal"

	// VisibilityBoth indicates events visible to both users and Everstack Cloud.
	// Stored in both user's local database AND dispatched to License Service.
	// Examples: license activations, limit warnings (sanitized versions).
	VisibilityBoth EventVisibility = "both"
)

// EventTypeVisibility maps event types to their visibility level
var EventTypeVisibility = map[string]EventVisibility{
	// User-facing audit events (configuration changes)
	"audit.configuration.changed":  VisibilityUser,
	"audit.load_balancer.changed":  VisibilityUser,
	"audit.provider.configured":    VisibilityUser,
	"audit.provider.deleted":       VisibilityUser,
	"api.key.created":              VisibilityUser,
	"api.key.revoked":              VisibilityUser,
	"api.key.rotated":              VisibilityUser,
	"model.configured":             VisibilityUser,
	"model.disabled":               VisibilityUser,
	"model.enabled":                VisibilityUser,
	"load_balancer.enabled":        VisibilityUser,
	"load_balancer.disabled":       VisibilityUser,
	"load_balancer.weights.updated": VisibilityUser,

	// Both user and Everstack Cloud (important lifecycle events)
	"instance.activated":           VisibilityBoth, // Gateway instance activated/upgraded
	"instance.activation_failed":   VisibilityBoth, // Gateway activation failed
	"license.activated":            VisibilityBoth,
	"license.upgraded":             VisibilityBoth,
	"license.downgraded":           VisibilityBoth,
	"license.expired":              VisibilityBoth,
	"license.renewed":              VisibilityBoth,
	"license.instance_data_missing": VisibilityBoth, // Tampering/data loss detection
	"usage.limit.warning":          VisibilityBoth,
	"usage.limit.exceeded":         VisibilityBoth,
	"gateway.locked":               VisibilityBoth,
	"gateway.unlocked":             VisibilityBoth,
	"fallback.triggered":           VisibilityBoth,
	"fallback.succeeded":           VisibilityBoth,
	"fallback.exhausted":           VisibilityBoth,
	"model.not_found":              VisibilityBoth,

	// Internal only (Everstack Cloud telemetry)
	"usage.request.completed":      VisibilityInternal,
	"chat.session.started":         VisibilityInternal,
	"chat.session.completed":       VisibilityInternal,
	"chat.session.error":           VisibilityInternal,
	"chat.message.processed":       VisibilityInternal,
	"model.selection.requested":    VisibilityInternal,
	"embedding.request.started":    VisibilityInternal,
	"embedding.request.completed":  VisibilityInternal,
	"provider.call.completed":      VisibilityInternal,
	"cache.hit":                    VisibilityInternal,
	"cache.miss":                   VisibilityInternal,
}

// GetVisibility returns the visibility level for an event type
// Defaults to VisibilityInternal if not explicitly mapped
func GetVisibility(eventType string) EventVisibility {
	if v, ok := EventTypeVisibility[eventType]; ok {
		return v
	}
	return VisibilityInternal
}

// IsVisibleToUser returns true if the event should be visible in user audit logs
func IsVisibleToUser(eventType string) bool {
	v := GetVisibility(eventType)
	return v == VisibilityUser || v == VisibilityBoth
}

// IsVisibleToCloud returns true if the event should be sent to Everstack Cloud
func IsVisibleToCloud(eventType string) bool {
	v := GetVisibility(eventType)
	return v == VisibilityInternal || v == VisibilityBoth
}

// RegisterEventVisibility allows registering custom event type visibility
// Useful for plugins or extensions
func RegisterEventVisibility(eventType string, visibility EventVisibility) {
	EventTypeVisibility[eventType] = visibility
}

