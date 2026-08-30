package firecracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

const guestAgentPort = 1024

// VsockClient communicates with the guest agent running inside a Firecracker VM.
// The guest agent listens on vsock port 1024 and accepts JSON-RPC commands.
type VsockClient struct {
	guestCID uint32
	udsPath  string // Unix domain socket path for vsock
}

// GuestCID returns the firecracker-side CID for this VM. Exposed so the
// backend's state.json persistence can record it for recovery after an
// agent restart.
func (c *VsockClient) GuestCID() uint32 { return c.guestCID }

// JSON-RPC message types for host↔guest communication.

// ExecCommand requests the guest agent to execute a command.
type ExecCommand = toolbox.ExecRequest

// ExecResponse is the guest agent's response to an exec command.
type ExecResponse = toolbox.ExecResponse

// WriteFileCommand requests the guest agent to write a file.
type WriteFileCommand = toolbox.WriteFileRequest

// ReadFileCommand requests the guest agent to read a file.
type ReadFileCommand = toolbox.ReadFileRequest

// ReadFileResponse is the guest agent's response to a read file command.
type ReadFileResponse = toolbox.ReadFileResponse

// ListFilesCommand requests the guest agent to list a directory.
type ListFilesCommand = toolbox.ListFilesRequest

// ListFilesResponse is the guest agent's response to a list files command.
type ListFilesResponse = toolbox.ListFilesResponse

// rpcRequest wraps a command for the JSON-RPC protocol.
type rpcRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

// rpcResponse wraps a response from the JSON-RPC protocol.
type rpcResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// NewVsockClient creates a new vsock client for the given guest CID.
func NewVsockClient(guestCID uint32, udsPath string) *VsockClient {
	return &VsockClient{
		guestCID: guestCID,
		udsPath:  udsPath,
	}
}

// WaitReady waits for the guest agent to signal readiness via vsock.
func (c *VsockClient) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := c.connect(1 * time.Second)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Send ping
		req := rpcRequest{Method: toolbox.MethodPing}
		encoder := json.NewEncoder(conn)
		decoder := json.NewDecoder(conn)

		if err := encoder.Encode(req); err != nil {
			conn.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}

		var resp rpcResponse
		if err := decoder.Decode(&resp); err != nil {
			conn.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}

		conn.Close()

		if resp.Error == "" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("guest agent not ready after %v", timeout)
}

// Exec executes a command in the guest VM.
func (c *VsockClient) Exec(ctx context.Context, cmd ExecCommand) (*sandbox.ExecResult, error) {
	resp, err := c.call(ctx, toolbox.MethodExec, cmd)
	if err != nil {
		return nil, err
	}

	var execResp ExecResponse
	if err := json.Unmarshal(resp, &execResp); err != nil {
		return nil, fmt.Errorf("failed to decode exec response: %w", err)
	}

	return &sandbox.ExecResult{
		ExitCode:   execResp.ExitCode,
		Stdout:     execResp.Stdout,
		Stderr:     execResp.Stderr,
		DurationMs: execResp.DurationMS,
		TimedOut:   execResp.TimedOut,
	}, nil
}

// WriteFile writes content to a file in the guest VM.
func (c *VsockClient) WriteFile(ctx context.Context, path string, content []byte) error {
	_, err := c.call(ctx, toolbox.MethodWriteFile, WriteFileCommand{
		Path:    path,
		Content: base64.StdEncoding.EncodeToString(content),
	})
	return err
}

// SetAgentToken pushes the per-VM :8080 bearer token to the guest agent over
// vsock (host-only channel). The guest then requires this token on every
// authenticated HTTP endpoint. Called at boot and re-pushed on recovery.
func (c *VsockClient) SetAgentToken(ctx context.Context, token string) error {
	_, err := c.call(ctx, toolbox.MethodSetAgentToken, toolbox.SetAgentTokenRequest{Token: token})
	return err
}

// ConfigureMountsCommand asks the guest agent to FUSE-mount the given storage
// mounts. Firecracker delivers no environment to the guest, so mounts (and
// their per-mount scoped credentials) are pushed over vsock after boot rather
// than read from SANDBOX_MOUNTS_JSON.
type ConfigureMountsCommand = toolbox.ConfigureMountsRequest

// ConfigureMounts pushes storage mounts to the guest agent.
func (c *VsockClient) ConfigureMounts(ctx context.Context, mounts []sandbox.StorageMountConfig) error {
	_, err := c.call(ctx, toolbox.MethodConfigureMounts, ConfigureMountsCommand{Mounts: toolboxMounts(mounts)})
	return err
}

func toolboxMounts(mounts []sandbox.StorageMountConfig) []toolbox.MountConfig {
	out := make([]toolbox.MountConfig, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, toolbox.MountConfig{
			Type:            m.Type,
			Bucket:          m.Bucket,
			MountPath:       m.MountPath,
			Endpoint:        m.Endpoint,
			SubPath:         m.SubPath,
			ReadOnly:        m.ReadOnly,
			AccessKeyID:     m.AccessKeyID,
			SecretAccessKey: m.SecretAccessKey,
			SessionToken:    m.SessionToken,
		})
	}
	return out
}

// ReadFile reads content from a file in the guest VM.
func (c *VsockClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.call(ctx, toolbox.MethodReadFile, ReadFileCommand{Path: path})
	if err != nil {
		return nil, err
	}

	var fileResp ReadFileResponse
	if err := json.Unmarshal(resp, &fileResp); err != nil {
		return nil, fmt.Errorf("failed to decode read_file response: %w", err)
	}

	content, err := base64.StdEncoding.DecodeString(fileResp.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

// ShellOpenParams matches the guest-side struct of the same name —
// kept in sync by hand because the guest agent and the host live in
// the same module but talk JSON-RPC over vsock (no shared schema).
type ShellOpenParams struct {
	// SessionID, if set, reattaches to an existing tmux session in
	// the guest. Empty creates a new session and the guest returns
	// its ID in ShellOpenResult.
	SessionID string            `json:"session_id,omitempty"`
	User      string            `json:"user,omitempty"`
	WorkDir   string            `json:"work_dir,omitempty"`
	Rows      uint16            `json:"rows,omitempty"`
	Cols      uint16            `json:"cols,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Shell     string            `json:"shell,omitempty"`
}

// ShellOpenResult is what the guest returns in the JSON-RPC handshake
// response before the conn switches to binary frames.
type ShellOpenResult struct {
	SessionID  string `json:"session_id"`
	Reattached bool   `json:"reattached"`
}

// ErrSessionGone is returned by OpenShell when the host requested a
// specific session_id that doesn't exist in the guest. Callers
// surface this as "your session ended, start a new one" — the
// alternative (silently creating a fresh session) is worse because
// the user would think they're resuming when they're not.
var ErrSessionGone = fmt.Errorf("shell session is gone")

// SessionInfo mirrors the guest agent's response struct for
// session_list. Used by the host to repopulate its in-memory session
// registry on fcagent restart and to render the admin UI's session
// list per sandbox.
type SessionInfo = toolbox.SessionInfo

// SessionListResponse is the guest agent's response to session_list.
type SessionListResponse = toolbox.SessionListResponse

// SessionKillCommand is the host's request to terminate a session.
type SessionKillCommand = toolbox.SessionKillRequest

// OpenShell starts a PTY-backed interactive shell in the guest and
// returns the raw vsock conn plus session metadata. After the
// JSON-RPC init handshake the conn carries shellframe binary frames
// in both directions — callers should construct a firecrackerShellConn
// around it rather than read from it directly.
//
// The returned conn is owned by the caller. Closing it detaches this
// client from the underlying tmux session but the session itself
// keeps running; tear down explicitly via KillSession when truly done.
//
// When params.SessionID is set but the session doesn't exist on the
// guest, OpenShell returns ErrSessionGone so the caller can surface
// "your session ended" instead of silently starting a fresh shell.
func (c *VsockClient) OpenShell(ctx context.Context, params ShellOpenParams) (net.Conn, ShellOpenResult, error) {
	conn, err := c.connect(5 * time.Second)
	if err != nil {
		return nil, ShellOpenResult{}, fmt.Errorf("vsock connect: %w", err)
	}

	// The init exchange uses the same JSON-RPC envelope as every other
	// method — guest dispatches on req.Method == "shell_open". A
	// successful response means the guest has the PTY allocated and
	// will start emitting TypeStdout frames; from this point the conn
	// is binary.
	// 30s default for the handshake: shell_open does non-trivial work
	// inside the guest (tmux session create, attach client spawn,
	// optional reattach lookup). The previous 10s default was tight
	// enough that one slow-cold call would blow the deadline, the host
	// would close, the client would auto-reconnect, and orphan tmux
	// sessions piled up on the guest. The actual happy-path now is
	// ~50ms (no sudo / PAM in the agent — see resolveSandboxUserCred);
	// 30s gives us headroom for kernel jitter without ever masking a
	// genuine hang.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(rpcRequest{Method: toolbox.MethodShellOpen, Params: params}); err != nil {
		conn.Close()
		return nil, ShellOpenResult{}, fmt.Errorf("vsock shell_open send: %w", err)
	}
	dec := json.NewDecoder(conn)
	var resp rpcResponse
	if err := dec.Decode(&resp); err != nil {
		conn.Close()
		return nil, ShellOpenResult{}, fmt.Errorf("vsock shell_open recv: %w", err)
	}
	if resp.Error != "" {
		conn.Close()
		// The guest emits the exact string "session_gone" when a
		// requested session_id isn't found. Translate to a typed error
		// here so callers don't have to string-match on the wire form.
		if resp.Error == "session_gone" {
			return nil, ShellOpenResult{}, ErrSessionGone
		}
		return nil, ShellOpenResult{}, fmt.Errorf("guest shell_open: %s", resp.Error)
	}

	// Decode the structured response. The guest now returns a
	// {session_id, reattached} object; older guests that pre-date the
	// session manager returned the plain string "ok" — handle both
	// shapes so a host upgrade ahead of the rootfs upgrade doesn't
	// brick every existing sandbox.
	var result ShellOpenResult
	if resp.Result != nil {
		raw, mErr := json.Marshal(resp.Result)
		if mErr == nil {
			_ = json.Unmarshal(raw, &result)
		}
	}

	// Clear the deadline before handing the conn back — interactive
	// shells legitimately sit idle for minutes, and the read pump must
	// not get aborted by the handshake deadline.
	_ = conn.SetDeadline(time.Time{})

	// json.Decoder may have buffered bytes past the response object.
	// In practice this is just the trailing newline (json.Encoder always
	// writes one) which we want to discard before reading binary frames.
	// Drain any buffered bytes; anything that survives is whitespace.
	if buffered := dec.Buffered(); buffered != nil {
		drainJSONTrailer(buffered)
	}
	return conn, result, nil
}

// ListSessions returns the persistent shell sessions alive inside the
// guest VM. fcagent calls this on startup to repopulate its session
// registry by walking each known VM. The full response (including
// the guest's reported NowUnix) is available via ListSessionsRaw.
func (c *VsockClient) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	resp, err := c.ListSessionsRaw(ctx)
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

// ListSessionsRaw is like ListSessions but exposes the full response
// envelope, including NowUnix. The session reaper uses NowUnix to
// compute idle duration in guest-clock terms — avoids the long-tail
// host/guest skew that accumulates on multi-day VMs.
func (c *VsockClient) ListSessionsRaw(ctx context.Context) (SessionListResponse, error) {
	raw, err := c.call(ctx, toolbox.MethodSessionList, struct{}{})
	if err != nil {
		return SessionListResponse{}, err
	}
	var resp SessionListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return SessionListResponse{}, fmt.Errorf("decode session_list: %w", err)
	}
	return resp, nil
}

// KillSession terminates a persistent shell session inside the guest.
// Idempotent — killing a missing session is treated as success.
func (c *VsockClient) KillSession(ctx context.Context, sessionID string) error {
	if _, err := c.call(ctx, toolbox.MethodSessionKill, SessionKillCommand{SessionID: sessionID}); err != nil {
		return err
	}
	return nil
}

// drainJSONTrailer reads from r until it hits non-whitespace or EOF.
// Used to flush the LF that json.Encoder writes after every object so
// that subsequent binary frame reads aren't preceded by a stray 0x0a.
func drainJSONTrailer(r io.Reader) {
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if err != nil || n == 0 {
			return
		}
		switch buf[0] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			// Non-whitespace — guest sent a binary frame eagerly.
			// We can't push the byte back into the underlying conn,
			// but in practice the encoder/decoder ordering guarantees
			// this branch is unreachable.
			return
		}
	}
}

// ListFiles lists directory contents in the guest VM.
func (c *VsockClient) ListFiles(ctx context.Context, path string) ([]sandbox.FileInfo, error) {
	resp, err := c.call(ctx, toolbox.MethodListFiles, ListFilesCommand{Path: path})
	if err != nil {
		return nil, err
	}

	var listResp ListFilesResponse
	if err := json.Unmarshal(resp, &listResp); err != nil {
		return nil, fmt.Errorf("failed to decode list_files response: %w", err)
	}

	files := make([]sandbox.FileInfo, 0, len(listResp.Files))
	for _, f := range listResp.Files {
		files = append(files, sandbox.FileInfo{
			Name:  f.Name,
			Path:  f.Path,
			Size:  f.Size,
			IsDir: f.IsDir,
		})
	}
	return files, nil
}

// call makes a JSON-RPC call to the guest agent over vsock.
func (c *VsockClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	conn, err := c.connect(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to guest agent: %w", err)
	}
	defer conn.Close()

	// Set deadline from context
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	req := rpcRequest{Method: method, Params: params}
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	var resp rpcResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("guest agent error: %s", resp.Error)
	}

	return resp.Result, nil
}

// connect dials the Firecracker host-side vsock UDS and performs the
// host-initiated CONNECT handshake. Firecracker's UDS proxy accepts
// "CONNECT <port>\n" on the configured uds_path and bridges the byte
// stream to an AF_VSOCK listener in the guest on that port. It replies
// with "OK <assigned_port>\n" on success or closes the connection on
// failure.
//
// See: https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md
func (c *VsockClient) connect(timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", c.udsPath, timeout)
	if err != nil {
		return nil, err
	}

	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", guestAgentPort); err != nil {
		conn.Close()
		return nil, fmt.Errorf("vsock CONNECT write: %w", err)
	}

	// Read the reply one byte at a time so we don't consume any bytes
	// past the newline — subsequent readers (JSON decoder) need the raw
	// connection, not a buffered wrapper.
	line, err := readLineUnbuffered(conn, 64)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("vsock CONNECT read: %w", err)
	}

	if !strings.HasPrefix(line, "OK ") {
		conn.Close()
		return nil, fmt.Errorf("vsock CONNECT rejected: %q", line)
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// readLineUnbuffered reads bytes from conn one at a time until it hits '\n'
// or the max cap. It returns the line without the trailing newline. Reading
// byte-by-byte avoids pulling bytes past the newline into a buffer, which
// matters when the caller will hand the raw conn to a JSON decoder.
func readLineUnbuffered(conn net.Conn, max int) (string, error) {
	buf := make([]byte, 0, max)
	one := make([]byte, 1)
	for len(buf) < max {
		n, err := conn.Read(one)
		if err != nil {
			return string(buf), err
		}
		if n == 0 {
			continue
		}
		if one[0] == '\n' {
			return string(buf), nil
		}
		buf = append(buf, one[0])
	}
	return string(buf), fmt.Errorf("line exceeded %d bytes", max)
}
