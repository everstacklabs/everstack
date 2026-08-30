package sandbox

import "testing"

func TestPublicLifecycleState(t *testing.T) {
	tests := []struct {
		name           string
		lifecycleState string
		status         Status
		want           string
	}{
		{name: "pending", lifecycleState: "pending", want: PublicLifecycleCreating},
		{name: "provisioning", lifecycleState: "provisioning", want: PublicLifecycleCreating},
		{name: "repo provisioning", lifecycleState: LifecycleRepoProvisioning, want: PublicLifecycleCreating},
		{name: "running", lifecycleState: LifecycleRunning, want: PublicLifecycleRunning},
		{name: "stopping", lifecycleState: LifecycleStopping, want: PublicLifecycleStopping},
		{name: "sleeping", lifecycleState: "sleeping", want: PublicLifecycleStopped},
		{name: "stopped", lifecycleState: LifecycleStopped, want: PublicLifecycleStopped},
		{name: "reviving", lifecycleState: LifecycleReviving, want: PublicLifecycleRestoring},
		{name: "restoring", lifecycleState: "restoring", want: PublicLifecycleRestoring},
		{name: "archiving", lifecycleState: "archiving", want: PublicLifecycleArchiving},
		{name: "archived", lifecycleState: "archived", want: PublicLifecycleArchived},
		{name: "terminating", lifecycleState: LifecycleTerminating, want: PublicLifecycleDeleting},
		{name: "terminated", lifecycleState: LifecycleTerminated, want: PublicLifecycleDeleted},
		{name: "failed", lifecycleState: LifecycleFailed, want: PublicLifecycleFailed},
		{name: "status fallback", status: StatusStopped, want: PublicLifecycleStopped},
		{name: "trim and lower", lifecycleState: " Reviving ", want: PublicLifecycleRestoring},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PublicLifecycleState(tt.lifecycleState, tt.status); got != tt.want {
				t.Fatalf("PublicLifecycleState(%q, %q) = %q, want %q", tt.lifecycleState, tt.status, got, tt.want)
			}
		})
	}
}
