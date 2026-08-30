// Package toolbox defines the in-sandbox agent toolbox wire contract.
//
// The host and cmd/sandbox-agent share these method names and payload shapes
// while transports migrate from legacy vsock JSON-RPC to the HTTP control
// plane. Keep this package dependency-light so the guest binary can import it.
package toolbox

const (
	MethodPing            = "ping"
	MethodExec            = "exec"
	MethodWriteFile       = "write_file"
	MethodReadFile        = "read_file"
	MethodListFiles       = "list_files"
	MethodSessionCreate   = "session_create"
	MethodSessionList     = "session_list"
	MethodSessionKill     = "session_kill"
	MethodShellOpen       = "shell_open"
	MethodConfigureMounts = "configure_mounts"
	// MethodSetAgentToken pushes the per-VM :8080 bearer token from the host
	// to the guest over the (host-only) vsock channel. The guest stores it and
	// requires it on every authenticated HTTP endpoint.
	MethodSetAgentToken = "set_agent_token"
)

// SetAgentTokenRequest carries the per-VM auth token pushed host -> guest over
// vsock. The token is a random 32-byte hex string, distinct per VM.
type SetAgentTokenRequest struct {
	Token string `json:"token"`
}

type ExecRequest struct {
	ID        string            `json:"id"`
	Command   []string          `json:"command"`
	WorkDir   string            `json:"work_dir"`
	Env       map[string]string `json:"env"`
	TimeoutMS int               `json:"timeout_ms"`
}

type ExecResponse struct {
	ID         string `json:"id"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

type WriteFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ReadFileRequest struct {
	Path string `json:"path"`
}

type ReadFileResponse struct {
	Content string `json:"content"`
	Size    int64  `json:"size"`
}

type ListFilesRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

type ListFilesResponse struct {
	Files []FileInfo `json:"files"`
}

type SessionListResponse struct {
	Sessions []SessionInfo `json:"sessions"`
	NowUnix  int64         `json:"now_unix"`
}

type SessionInfo struct {
	ID               string `json:"id"`
	Attached         bool   `json:"attached"`
	CreatedUnix      int64  `json:"created_unix"`
	LastActivityUnix int64  `json:"last_activity_unix"`
}

type SessionKillRequest struct {
	SessionID string `json:"session_id"`
}

type MountConfig struct {
	Type      string `json:"type"`
	Bucket    string `json:"bucket"`
	MountPath string `json:"mount_path"`
	Endpoint  string `json:"endpoint,omitempty"`
	SubPath   string `json:"subpath,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`

	// Per-mount S3/R2 credentials. The guest applies these only to the mount
	// subprocess env so unrelated execs do not inherit object-store credentials.
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
}

type ConfigureMountsRequest struct {
	Mounts []MountConfig `json:"mounts"`
}
