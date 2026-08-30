package firecracker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

var ErrToolboxHTTPUnavailable = errors.New("toolbox HTTP unavailable")

const defaultToolboxHTTPTimeout = 30 * time.Second

type toolboxHTTPClient struct {
	baseURL string
	client  *http.Client
	token   string // per-VM bearer token sent as Authorization on every request
}

type toolboxHTTPResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func newToolboxHTTPClientForGuest(guestIP, token string, timeout time.Duration) (*toolboxHTTPClient, bool) {
	if guestIP == "" {
		return nil, false
	}
	if timeout <= 0 {
		timeout = defaultToolboxHTTPTimeout
	}
	host := net.JoinHostPort(guestIP, strconv.Itoa(healthProbePort))
	return &toolboxHTTPClient{
		baseURL: "http://" + host,
		client:  &http.Client{Timeout: timeout},
		token:   token,
	}, true
}

func (vm *MicroVM) toolboxHTTPClient(timeout time.Duration) (*toolboxHTTPClient, bool) {
	if vm == nil || vm.Network == nil || vm.Network.GuestIP == "" {
		return nil, false
	}
	return newToolboxHTTPClientForGuest(vm.Network.GuestIP, vm.AgentToken, timeout)
}

func (vm *MicroVM) ToolboxExec(ctx context.Context, cmd ExecCommand) (*sandbox.ExecResult, error) {
	timeout := time.Duration(cmd.TimeoutMS)*time.Millisecond + 5*time.Second
	if c, ok := vm.toolboxHTTPClient(timeout); ok {
		result, err := c.Exec(ctx, cmd)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return result, err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodExec, err)
	}
	if vm == nil || vm.Vsock == nil {
		return nil, fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	return vm.Vsock.Exec(ctx, cmd)
}

func (vm *MicroVM) ToolboxWriteFile(ctx context.Context, path string, content []byte) error {
	if c, ok := vm.toolboxHTTPClient(defaultToolboxHTTPTimeout); ok {
		err := c.WriteFile(ctx, path, content)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodWriteFile, err)
	}
	if vm == nil || vm.Vsock == nil {
		return fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	return vm.Vsock.WriteFile(ctx, path, content)
}

func (vm *MicroVM) ToolboxReadFile(ctx context.Context, path string) ([]byte, error) {
	if c, ok := vm.toolboxHTTPClient(defaultToolboxHTTPTimeout); ok {
		content, err := c.ReadFile(ctx, path)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return content, err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodReadFile, err)
	}
	if vm == nil || vm.Vsock == nil {
		return nil, fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	return vm.Vsock.ReadFile(ctx, path)
}

func (vm *MicroVM) ToolboxListFiles(ctx context.Context, path string) ([]sandbox.FileInfo, error) {
	if c, ok := vm.toolboxHTTPClient(defaultToolboxHTTPTimeout); ok {
		files, err := c.ListFiles(ctx, path)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return files, err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodListFiles, err)
	}
	if vm == nil || vm.Vsock == nil {
		return nil, fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	return vm.Vsock.ListFiles(ctx, path)
}

func (vm *MicroVM) ToolboxConfigureMounts(ctx context.Context, mounts []sandbox.StorageMountConfig) error {
	if c, ok := vm.toolboxHTTPClient(defaultToolboxHTTPTimeout); ok {
		err := c.ConfigureMounts(ctx, mounts)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodConfigureMounts, err)
	}
	if vm == nil || vm.Vsock == nil {
		return fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	if err := vm.Vsock.WaitReady(ctx, 10*time.Second); err != nil {
		return err
	}
	return vm.Vsock.ConfigureMounts(ctx, mounts)
}

func (vm *MicroVM) ToolboxListSessionsRaw(ctx context.Context) (SessionListResponse, error) {
	if c, ok := vm.toolboxHTTPClient(defaultToolboxHTTPTimeout); ok {
		resp, err := c.ListSessionsRaw(ctx)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return resp, err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodSessionList, err)
	}
	if vm == nil || vm.Vsock == nil {
		return SessionListResponse{}, fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	return vm.Vsock.ListSessionsRaw(ctx)
}

func (vm *MicroVM) ToolboxListSessions(ctx context.Context) ([]SessionInfo, error) {
	resp, err := vm.ToolboxListSessionsRaw(ctx)
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

func (vm *MicroVM) ToolboxKillSession(ctx context.Context, sessionID string) error {
	if c, ok := vm.toolboxHTTPClient(defaultToolboxHTTPTimeout); ok {
		err := c.KillSession(ctx, sessionID)
		if err == nil || !errors.Is(err, ErrToolboxHTTPUnavailable) {
			return err
		}
		logToolboxHTTPFallback(vm.ID, toolbox.MethodSessionKill, err)
	}
	if vm == nil || vm.Vsock == nil {
		return fmt.Errorf("firecracker_toolbox: vsock unavailable")
	}
	return vm.Vsock.KillSession(ctx, sessionID)
}

func (c *toolboxHTTPClient) Exec(ctx context.Context, req toolbox.ExecRequest) (*sandbox.ExecResult, error) {
	var resp toolbox.ExecResponse
	if err := c.call(ctx, toolbox.MethodExec, req, &resp); err != nil {
		return nil, err
	}
	return &sandbox.ExecResult{
		ExitCode:   resp.ExitCode,
		Stdout:     resp.Stdout,
		Stderr:     resp.Stderr,
		DurationMs: resp.DurationMS,
		TimedOut:   resp.TimedOut,
	}, nil
}

func (c *toolboxHTTPClient) WriteFile(ctx context.Context, path string, content []byte) error {
	return c.call(ctx, toolbox.MethodWriteFile, toolbox.WriteFileRequest{
		Path:    path,
		Content: base64.StdEncoding.EncodeToString(content),
	}, nil)
}

func (c *toolboxHTTPClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	var resp toolbox.ReadFileResponse
	if err := c.call(ctx, toolbox.MethodReadFile, toolbox.ReadFileRequest{Path: path}, &resp); err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("decode toolbox file content: %w", err)
	}
	return content, nil
}

func (c *toolboxHTTPClient) ListFiles(ctx context.Context, path string) ([]sandbox.FileInfo, error) {
	var resp toolbox.ListFilesResponse
	if err := c.call(ctx, toolbox.MethodListFiles, toolbox.ListFilesRequest{Path: path}, &resp); err != nil {
		return nil, err
	}
	files := make([]sandbox.FileInfo, 0, len(resp.Files))
	for _, f := range resp.Files {
		files = append(files, sandbox.FileInfo{
			Name:  f.Name,
			Path:  f.Path,
			Size:  f.Size,
			IsDir: f.IsDir,
		})
	}
	return files, nil
}

func (c *toolboxHTTPClient) ConfigureMounts(ctx context.Context, mounts []sandbox.StorageMountConfig) error {
	return c.call(ctx, toolbox.MethodConfigureMounts, toolbox.ConfigureMountsRequest{Mounts: toolboxMounts(mounts)}, nil)
}

func (c *toolboxHTTPClient) ListSessionsRaw(ctx context.Context) (SessionListResponse, error) {
	var resp toolbox.SessionListResponse
	if err := c.call(ctx, toolbox.MethodSessionList, struct{}{}, &resp); err != nil {
		return SessionListResponse{}, err
	}
	out := SessionListResponse{NowUnix: resp.NowUnix, Sessions: make([]SessionInfo, 0, len(resp.Sessions))}
	for _, s := range resp.Sessions {
		out.Sessions = append(out.Sessions, SessionInfo{
			ID:               s.ID,
			Attached:         s.Attached,
			CreatedUnix:      s.CreatedUnix,
			LastActivityUnix: s.LastActivityUnix,
		})
	}
	return out, nil
}

func (c *toolboxHTTPClient) KillSession(ctx context.Context, sessionID string) error {
	return c.call(ctx, toolbox.MethodSessionKill, toolbox.SessionKillRequest{SessionID: sessionID}, nil)
}

func (c *toolboxHTTPClient) call(ctx context.Context, method string, reqBody any, out any) error {
	endpoint, err := toolboxEndpoint(method)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal toolbox request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build toolbox HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrToolboxHTTPUnavailable, err)
	}
	defer resp.Body.Close()

	var envelope toolboxHTTPResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("%w: decode response: %v", ErrToolboxHTTPUnavailable, err)
	}
	if envelope.Error != "" {
		return fmt.Errorf("guest agent error: %s", envelope.Error)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Log 401 distinctly from a transport failure. Falling back to vsock
		// keeps the call working (vsock is host-only), but a PERSISTENT 401
		// means the host/guest token drifted and :8080 is silently unused —
		// otherwise invisible. Surface it so an auth regression is diagnosable.
		logger.WithFields("url", c.baseURL, "endpoint", endpoint).
			Warn("firecracker_toolbox: :8080 auth rejected (token mismatch); falling back to vsock")
		return fmt.Errorf("%w: unauthorized (401)", ErrToolboxHTTPUnavailable)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d", ErrToolboxHTTPUnavailable, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode toolbox result: %w", err)
	}
	return nil
}

func toolboxEndpoint(method string) (string, error) {
	switch method {
	case toolbox.MethodExec:
		return "/toolbox/exec", nil
	case toolbox.MethodWriteFile:
		return "/toolbox/files/write", nil
	case toolbox.MethodReadFile:
		return "/toolbox/files/read", nil
	case toolbox.MethodListFiles:
		return "/toolbox/files/list", nil
	case toolbox.MethodSessionList:
		return "/toolbox/sessions/list", nil
	case toolbox.MethodSessionKill:
		return "/toolbox/sessions/kill", nil
	case toolbox.MethodConfigureMounts:
		return "/toolbox/mounts/configure", nil
	default:
		return "", fmt.Errorf("unsupported toolbox HTTP method %q", method)
	}
}

func logToolboxHTTPFallback(sandboxID, method string, err error) {
	logger.WithFields("sandbox_id", sandboxID, "method", method, "error", err.Error()).
		Debug("firecracker_toolbox: HTTP unavailable, falling back to vsock")
}
