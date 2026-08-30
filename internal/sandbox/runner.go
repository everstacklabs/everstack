package sandbox

// RunnerPlacement describes where a sandbox runner executes workloads.
type RunnerPlacement string

const (
	RunnerPlacementUnknown     RunnerPlacement = "unknown"
	RunnerPlacementLocal       RunnerPlacement = "local"
	RunnerPlacementCluster     RunnerPlacement = "cluster"
	RunnerPlacementRemoteAgent RunnerPlacement = "remote_agent"
)

// RunnerHealthModel describes the primary health signal for a runner.
type RunnerHealthModel string

const (
	RunnerHealthUnknown       RunnerHealthModel = "unknown"
	RunnerHealthDockerDaemon  RunnerHealthModel = "docker_daemon"
	RunnerHealthKubernetesAPI RunnerHealthModel = "kubernetes_api"
	RunnerHealthInGuestAgent  RunnerHealthModel = "in_guest_agent"
	RunnerHealthRemoteAgent   RunnerHealthModel = "remote_agent"
)

// RunnerCapabilities names feature support explicitly so control-plane code does
// not infer behavior from backend names.
type RunnerCapabilities struct {
	Target    string
	Placement RunnerPlacement
	Health    RunnerHealthModel
	Capacity  RunnerCapacity
	Features  RunnerFeatures
}

type RunnerCapacity struct {
	MaxInstances  int
	WarmPoolSize  int
	WarmPoolLimit int
}

type RunnerFeatures struct {
	WorkspaceSnapshot bool
	DockerCPSnapshot  bool
	VMSnapshot        bool
	VMRestore         bool
	GitImport         bool
	PortExposure      bool
	PortDetection     bool
	PersistentShell   bool
	SSH               bool
	Volumes           bool
	ComputerUse       bool
	Resize            bool
}

// RunnerCapabilityProvider is optionally implemented by sandbox backends that
// can report explicit runner metadata.
type RunnerCapabilityProvider interface {
	RunnerCapabilities() RunnerCapabilities
}

// CapabilitiesForBackend returns explicit backend capabilities when available
// and falls back to optional interface detection for older test/mocked backends.
func CapabilitiesForBackend(b Backend) RunnerCapabilities {
	if b == nil {
		return RunnerCapabilities{Placement: RunnerPlacementUnknown, Health: RunnerHealthUnknown}
	}

	var caps RunnerCapabilities
	if provider, ok := b.(RunnerCapabilityProvider); ok {
		caps = provider.RunnerCapabilities()
	}
	if caps.Target == "" {
		caps.Target = b.Name()
	}
	if caps.Placement == "" {
		caps.Placement = RunnerPlacementUnknown
	}
	if caps.Health == "" {
		caps.Health = RunnerHealthUnknown
	}

	if _, ok := b.(Snapshotter); ok {
		caps.Features.WorkspaceSnapshot = true
	}
	if _, ok := b.(VMSnapshotter); ok {
		caps.Features.VMSnapshot = true
	}
	if _, ok := b.(VMRestorer); ok {
		caps.Features.VMRestore = true
	}
	if _, ok := b.(PortExposer); ok {
		caps.Features.PortExposure = true
	}
	if _, ok := b.(PortDetector); ok {
		caps.Features.PortDetection = true
	}
	if _, ok := b.(PersistentShellBackend); ok {
		caps.Features.PersistentShell = true
	}

	return caps
}
