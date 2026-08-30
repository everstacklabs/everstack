// sandbox-agent is a lightweight agent that runs inside Firecracker microVMs.
// It listens on two channels:
//
//   - vsock port 1024 — JSON-RPC for commands from the host. Used during
//     early boot (before eth0 is up) and as a fallback. Will be migrated
//     away from in later phases.
//   - TCP :8080 — HTTP control plane. Liveness probes, and eventually
//     exec/logs/metrics/shells/ports. Reachable from the host via the
//     guest's TAP IP. Modeled after e2b's envd — liveness lives on its
//     own transport so a wedged vsock can't make a healthy agent look
//     dead.
//
// Build: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o sandbox-agent ./cmd/sandbox-agent/
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

const (
	listenPort     = 1024
	httpListenPort = 8080
	maxOutput      = 1 << 20 // 1MB max output per command
)

type rpcRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type execCommand = toolbox.ExecRequest
type execResponse = toolbox.ExecResponse
type writeFileCommand = toolbox.WriteFileRequest
type readFileCommand = toolbox.ReadFileRequest
type readFileResponse = toolbox.ReadFileResponse
type listFilesCommand = toolbox.ListFilesRequest
type fileInfo = toolbox.FileInfo
type listFilesResponse = toolbox.ListFilesResponse
type mountConfig = toolbox.MountConfig

func main() {
	// Listen on vsock. For Firecracker, vsock is exposed as a virtio device.
	// The listener binds to CID=any, port=1024.
	listener, err := listenVsock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent: failed to listen: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Fprintf(os.Stderr, "sandbox-agent: listening on port %d\n", listenPort)

	// Apply egress network policy (SANDBOX_NETWORK_BLOCK_ALL + SANDBOX_NETWORK_ALLOW_CIDRS).
	// Runs synchronously before any user traffic can flow so the policy is active from t=0
	// (i.e. before the HTTP control plane below starts serving exec/shell). Best-effort:
	// local iptables, non-blocking; failures are logged and the agent continues normally.
	applyNetworkPolicy()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Start the HTTP control plane FIRST — before any optional, network-dependent
	// setup below. Binds to 0.0.0.0 so the listener is up regardless of whether eth0
	// has been configured yet; once the host writes the network config over vsock and
	// brings eth0 up, the listener is immediately reachable over the TAP IP.
	//
	// This ordering is load-bearing for VM survival. The host's WaitForAgentReady probes
	// :8080 within 10s of boot, and its per-VM health loop reaps the VM at 5 minutes if
	// :8080 never answers. applyTailscaleVPN runs `tailscale up`, which BLOCKS until it
	// can reach the Tailscale control plane — but eth0 isn't up until the host configures
	// it AFTER boot. If that (or a slow rclone mount / Xvfb start) ran before this bind,
	// :8080 would never come up and every Tailscale/mount/computer-use sandbox would be
	// killed at 5m while systemd still showed the unit "active". Bind liveness first.
	// Failures to bind don't kill the vsock path; the existing command flow keeps working.
	httpServer := startHTTPServer(ctx)

	// Optional, feature-gated setup — each early-returns when its env var is unset. Run
	// off the critical path (original relative order preserved) so a slow or hanging init
	// can never delay the :8080 liveness bind above or the vsock accept loop below. The
	// feature endpoints guard themselves until ready (e.g. /computer/* returns 503 via
	// requireComputerUse until Xvfb is up), so serving before these finish is safe.
	go func() {
		// Join customer Tailnet if SANDBOX_TAILSCALE_AUTH_KEY is set.
		applyTailscaleVPN()
		// Mount external storage (S3, R2, GCS, Azure) from SANDBOX_MOUNTS_JSON.
		applyStorageMounts()
		// Start Computer Use (Xvfb + XFCE4) if SANDBOX_COMPUTER_USE=1.
		startComputerUse()
	}()

	// Pre-warm tmux + bash so the user's first shell_open lands on a
	// hot daemon with already-paged-in profile scripts. Fire-and-
	// forget; failures degrade to "first shell pays the cold cost
	// as before" rather than breaking anything.
	go warmupShellEnvironment()

	go func() {
		<-ctx.Done()
		listener.Close()
		if httpServer != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = httpServer.Shutdown(shutdownCtx)
			cancel()
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				fmt.Fprintf(os.Stderr, "sandbox-agent: accept error: %v\n", err)
				continue
			}
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req rpcRequest
	if err := decoder.Decode(&req); err != nil {
		if err != io.EOF {
			encoder.Encode(rpcResponse{Error: "invalid request: " + err.Error()})
		}
		return
	}

	// shell_open is a streaming method — after the JSON handshake the
	// conn carries shellframe binary frames and the normal request/
	// response flow doesn't apply.
	//
	// json.Encoder.Encode always appends a trailing LF, so after we
	// Decode the request the decoder's buffer holds at least one
	// whitespace byte. Left in front of postInit, that LF lands as
	// the first byte the frame parser reads — it gets interpreted
	// as a frame type and the next 4 bytes of the host's first real
	// frame shift into the length field. Length trips MaxPayload,
	// ReadFrame returns an error, and the guest tears down the
	// session the moment the host writes anything (initial resize
	// or the first keystroke). Symptom on the host: the WebSocket
	// drops on every keystroke. Strip leading whitespace from the
	// buffered tail so the first byte the frame parser sees is the
	// start of a real frame.
	if req.Method == toolbox.MethodShellOpen {
		buffered, _ := io.ReadAll(decoder.Buffered())
		trimmed := bytes.TrimLeft(buffered, " \t\r\n")
		var postInit io.Reader = conn
		if len(trimmed) > 0 {
			postInit = io.MultiReader(bytes.NewReader(trimmed), conn)
		}
		handleShellOpen(conn, postInit, encoder, req.Params)
		return
	}

	resp := handleRequest(req)
	encoder.Encode(resp)
}

func handleRequest(req rpcRequest) rpcResponse {
	switch req.Method {
	case toolbox.MethodPing:
		return rpcResponse{Result: "pong"}
	case toolbox.MethodExec:
		return handleExec(req.Params)
	case toolbox.MethodWriteFile:
		return handleWriteFile(req.Params)
	case toolbox.MethodReadFile:
		return handleReadFile(req.Params)
	case toolbox.MethodListFiles:
		return handleListFiles(req.Params)
	case toolbox.MethodSessionCreate:
		return handleSessionCreate(req.Params)
	case toolbox.MethodSessionList:
		return handleSessionList(req.Params)
	case toolbox.MethodSessionKill:
		return handleSessionKill(req.Params)
	case toolbox.MethodConfigureMounts:
		return handleConfigureMounts(req.Params)
	case toolbox.MethodSetAgentToken:
		return handleSetAgentToken(req.Params)
	default:
		return rpcResponse{Error: fmt.Sprintf("unknown method: %s", req.Method)}
	}
}

// handleConfigureMounts applies storage mounts pushed by the host over vsock.
// This is how Firecracker guests get mounts: there's no env-injection path, so
// SANDBOX_MOUNTS_JSON is empty in the VM and the host delivers mounts (incl.
// per-mount scoped creds) here after boot.
func handleConfigureMounts(params json.RawMessage) rpcResponse {
	var cmd toolbox.ConfigureMountsRequest
	if err := json.Unmarshal(params, &cmd); err != nil {
		return rpcResponse{Error: "invalid configure_mounts params: " + err.Error()}
	}
	for _, m := range cmd.Mounts {
		if err := applyMount(m); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox-agent: mount %s@%s: %v\n", m.Type, m.MountPath, err)
		}
	}
	return rpcResponse{Result: "ok"}
}

// handleSetAgentToken stores the per-VM :8080 bearer token pushed by the host
// over vsock. vsock is host<->guest only (per-VM CID + host-side UDS, no
// inter-VM routing), so this delivery channel can't be observed by a peer guest.
func handleSetAgentToken(params json.RawMessage) rpcResponse {
	var cmd toolbox.SetAgentTokenRequest
	if err := json.Unmarshal(params, &cmd); err != nil {
		return rpcResponse{Error: "invalid set_agent_token params: " + err.Error()}
	}
	if cmd.Token == "" {
		return rpcResponse{Error: "set_agent_token: empty token"}
	}
	setAgentToken(cmd.Token)
	return rpcResponse{Result: "ok"}
}

func handleExec(params json.RawMessage) rpcResponse {
	var cmd execCommand
	if err := json.Unmarshal(params, &cmd); err != nil {
		return rpcResponse{Error: "invalid exec params: " + err.Error()}
	}

	if len(cmd.Command) == 0 {
		return rpcResponse{Error: "command is required"}
	}

	timeout := 30 * time.Second
	if cmd.TimeoutMS > 0 {
		timeout = time.Duration(cmd.TimeoutMS) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()

	c := exec.CommandContext(ctx, cmd.Command[0], cmd.Command[1:]...)
	if cmd.WorkDir != "" {
		c.Dir = cmd.WorkDir
	}

	// Set environment
	c.Env = os.Environ()
	for k, v := range cmd.Env {
		c.Env = append(c.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Run as non-root user if available
	// (guest images should have a "sandbox" user)

	var stdout, stderr strings.Builder
	c.Stdout = &limitedWriter{w: &stdout, max: maxOutput}
	c.Stderr = &limitedWriter{w: &stderr, max: maxOutput}

	err := c.Run()
	duration := time.Since(start).Milliseconds()

	exitCode := 0
	timedOut := false

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return rpcResponse{Error: "exec failed: " + err.Error()}
		}
	}

	return rpcResponse{Result: execResponse{
		ID:         cmd.ID,
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: duration,
		TimedOut:   timedOut,
	}}
}

func handleWriteFile(params json.RawMessage) rpcResponse {
	var cmd writeFileCommand
	if err := json.Unmarshal(params, &cmd); err != nil {
		return rpcResponse{Error: "invalid write_file params: " + err.Error()}
	}

	content, err := base64.StdEncoding.DecodeString(cmd.Content)
	if err != nil {
		return rpcResponse{Error: "invalid base64 content: " + err.Error()}
	}

	// Ensure parent directory exists
	dir := filepath.Dir(cmd.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return rpcResponse{Error: "failed to create directory: " + err.Error()}
	}

	if err := os.WriteFile(cmd.Path, content, 0644); err != nil {
		return rpcResponse{Error: "failed to write file: " + err.Error()}
	}

	return rpcResponse{Result: "ok"}
}

func handleReadFile(params json.RawMessage) rpcResponse {
	var cmd readFileCommand
	if err := json.Unmarshal(params, &cmd); err != nil {
		return rpcResponse{Error: "invalid read_file params: " + err.Error()}
	}

	info, err := os.Stat(cmd.Path)
	if err != nil {
		return rpcResponse{Error: "file not found: " + err.Error()}
	}

	if info.Size() > maxOutput {
		return rpcResponse{Error: fmt.Sprintf("file too large: %d bytes (max %d)", info.Size(), maxOutput)}
	}

	content, err := os.ReadFile(cmd.Path)
	if err != nil {
		return rpcResponse{Error: "failed to read file: " + err.Error()}
	}

	return rpcResponse{Result: readFileResponse{
		Content: base64.StdEncoding.EncodeToString(content),
		Size:    info.Size(),
	}}
}

func handleListFiles(params json.RawMessage) rpcResponse {
	var cmd listFilesCommand
	if err := json.Unmarshal(params, &cmd); err != nil {
		return rpcResponse{Error: "invalid list_files params: " + err.Error()}
	}

	path := cmd.Path
	if path == "" {
		path = "/workspace"
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return rpcResponse{Error: "failed to list directory: " + err.Error()}
	}

	var files []fileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			Name:  entry.Name(),
			Path:  filepath.Join(path, entry.Name()),
			Size:  info.Size(),
			IsDir: entry.IsDir(),
		})
	}

	return rpcResponse{Result: listFilesResponse{Files: files}}
}

// limitedWriter caps the amount of data written.
type limitedWriter struct {
	w       *strings.Builder
	max     int
	written int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.max - lw.written
	if remaining <= 0 {
		return len(p), nil // Silently discard
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.written += n
	return n, err
}
