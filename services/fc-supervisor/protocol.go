package main

// Wire protocol for the host-side firecracker VM supervisor.
//
// Source of truth — the design doc points here. Two callers care
// about these structs: this binary itself (server) and fcagent's
// supervisor client (lives in internal/sandbox/fcagent — added in
// a follow-up PR). Keeping them in this package keeps the contract
// in one place; the client copies the types when integrated rather
// than importing this package (avoid cross-binary coupling).

// SpawnRequest is the body of POST /vms/spawn.
//
// All fields are required. The supervisor validates them up front
// and returns 400 on any missing value rather than letting
// systemd-run produce a less helpful error.
type SpawnRequest struct {
	SandboxID  string `json:"sandbox_id"`
	BinaryPath string `json:"binary_path"`
	APISocket  string `json:"api_sock"`
	WorkDir    string `json:"work_dir"`
	MemoryMB   int64  `json:"mem_mb"`
	VCPUs      int    `json:"vcpus"`
}

// SpawnResponse is the body of a successful POST /vms/spawn. The
// caller persists both fields: ScopeName goes into vmState so
// future Destroys can stop the scope by name; MainPID is the
// firecracker process pid for /proc-based health checks and the
// legacy Process.Pid path.
type SpawnResponse struct {
	ScopeName string `json:"scope_name"`
	MainPID   int    `json:"main_pid"`
}

// VMInfo is one entry in the GET /vms response. Matches what
// systemd reports for the scope unit.
type VMInfo struct {
	SandboxID string `json:"sandbox_id"`
	ScopeName string `json:"scope_name"`
	MainPID   int    `json:"main_pid"`
	WorkDir   string `json:"work_dir"`
}

// ListResponse wraps GET /vms.
type ListResponse struct {
	VMs []VMInfo `json:"vms"`
}

// ErrorResponse is the body of any non-2xx response with content.
// The status code is the primary signal; this struct gives the
// caller a human-readable detail for logs without parsing prose.
type ErrorResponse struct {
	Error string `json:"error"`
}
