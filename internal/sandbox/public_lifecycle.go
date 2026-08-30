package sandbox

import "strings"

const (
	PublicLifecycleCreating  = "creating"
	PublicLifecycleRunning   = "running"
	PublicLifecycleStopping  = "stopping"
	PublicLifecycleStopped   = "stopped"
	PublicLifecycleArchiving = "archiving"
	PublicLifecycleArchived  = "archived"
	PublicLifecycleRestoring = "restoring"
	PublicLifecycleDeleting  = "deleting"
	PublicLifecycleDeleted   = "deleted"
	PublicLifecycleFailed    = "failed"
)

// PublicLifecycleState maps internal manager/reconciler states to the public
// Daytona-style lifecycle vocabulary used by APIs and UI.
func PublicLifecycleState(lifecycleState string, status Status) string {
	s := strings.TrimSpace(lifecycleState)
	if s == "" {
		s = strings.TrimSpace(string(status))
	}
	s = strings.ToLower(s)
	switch s {
	case "pending", "provisioning", LifecycleRepoProvisioning:
		return PublicLifecycleCreating
	case "creating":
		return PublicLifecycleCreating
	case LifecycleRunning:
		return PublicLifecycleRunning
	case LifecycleStopping:
		return PublicLifecycleStopping
	case "sleeping", LifecycleStopped:
		return PublicLifecycleStopped
	case LifecycleReviving, "restoring":
		return PublicLifecycleRestoring
	case "archiving":
		return PublicLifecycleArchiving
	case "archived":
		return PublicLifecycleArchived
	case LifecycleTerminating:
		return PublicLifecycleDeleting
	case LifecycleTerminated:
		return PublicLifecycleDeleted
	case LifecycleFailed, "error":
		return PublicLifecycleFailed
	default:
		return s
	}
}

func statusForLifecycle(lifecycleState string, fallback Status) Status {
	s := strings.ToLower(strings.TrimSpace(lifecycleState))
	switch s {
	case "pending", "creating", "provisioning", LifecycleRepoProvisioning, LifecycleStopping, LifecycleReviving:
		return StatusPending
	case LifecycleRunning:
		return StatusRunning
	case "sleeping", LifecycleStopped, "archiving", "archived", LifecycleTerminating, LifecycleTerminated:
		return StatusStopped
	case LifecycleFailed, "error":
		return StatusFailed
	default:
		if fallback != "" {
			return fallback
		}
		return StatusPending
	}
}
