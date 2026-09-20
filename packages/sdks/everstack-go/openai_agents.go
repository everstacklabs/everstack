package everstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const openAIAgentsPath = "/v1/openai-agents"

// WithAccessToken authenticates with a device/OAuth bearer token. OpenAI
// Agents connection and session mutations require this user identity, while
// instance API keys remain suitable for read operations.
func WithAccessToken(token string) Option {
	return func(c *config) {
		headers := make(map[string]string, len(c.headers)+1)
		for key, value := range c.headers {
			headers[key] = value
		}
		headers["Authorization"] = "Bearer " + token
		c.headers = headers
	}
}

type OpenAIAgentConnection struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	ProjectID      string   `json:"project_id,omitempty"`
	OrganizationID string   `json:"organization_id,omitempty"`
	PrincipalID    string   `json:"principal_id,omitempty"`
	AllowedModels  []string `json:"allowed_models,omitempty"`
	Status         string   `json:"status"`
	LastError      string   `json:"last_error,omitempty"`
}

type OpenAIAgentConnectionParams struct {
	Name           string   `json:"name"`
	APIKey         string   `json:"api_key"`
	ExecutorKey    string   `json:"executor_key,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	OrganizationID string   `json:"organization_id,omitempty"`
	PrincipalID    string   `json:"principal_id,omitempty"`
	AllowedModels  []string `json:"allowed_models,omitempty"`
}

type OpenAIAgentConnectionUpdate struct {
	Name           *string   `json:"name,omitempty"`
	APIKey         *string   `json:"api_key,omitempty"`
	ExecutorKey    *string   `json:"executor_key,omitempty"`
	ProjectID      *string   `json:"project_id,omitempty"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	PrincipalID    *string   `json:"principal_id,omitempty"`
	AllowedModels  *[]string `json:"allowed_models,omitempty"`
}

type OpenAIAgentEnvironment struct {
	Type               string `json:"type"`
	WorkspaceDirectory string `json:"workspace_directory,omitempty"`
}

type OpenAIAgentSession struct {
	ID                    string                 `json:"id"`
	Provider              string                 `json:"provider"`
	ConnectionID          string                 `json:"connection_id"`
	UpstreamSessionID     string                 `json:"upstream_session_id,omitempty"`
	UpstreamEnvironmentID string                 `json:"upstream_environment_id,omitempty"`
	SandboxID             string                 `json:"sandbox_id,omitempty"`
	Model                 string                 `json:"model"`
	Environment           OpenAIAgentEnvironment `json:"environment"`
	Status                string                 `json:"status"`
	Readiness             string                 `json:"readiness"`
	CleanupStatus         string                 `json:"cleanup_status"`
	LastError             string                 `json:"last_error,omitempty"`
	CreatedAt             string                 `json:"created_at"`
	UpdatedAt             string                 `json:"updated_at"`
}

type OpenAIAgentSessionParams struct {
	ConnectionID   string                 `json:"connection_id"`
	IdempotencyKey string                 `json:"idempotency_key"`
	Model          string                 `json:"model"`
	Instructions   string                 `json:"instructions,omitempty"`
	Environment    OpenAIAgentEnvironment `json:"environment"`
	Input          string                 `json:"input,omitempty"`
	Tools          []OpenAIAgentTool      `json:"tools,omitempty"`
}

type OpenAIAgentTool struct {
	Type       string `json:"type"`
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
}

type OpenAIAgentInputParams struct {
	IdempotencyKey string `json:"idempotency_key"`
	Input          string `json:"input"`
}
type OpenAIAgentAccepted struct {
	Accepted bool `json:"accepted"`
}
type OpenAIAgentDeleted struct {
	Deleted bool `json:"deleted"`
}
type OpenAIAgentResolved struct {
	Resolved bool `json:"resolved"`
}
type OpenAIAgentEvent struct {
	Cursor    int64           `json:"cursor"`
	EventID   string          `json:"event_id"`
	Type      string          `json:"type"`
	Version   string          `json:"version"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}
type OpenAIAgentEventPage struct {
	Data       []OpenAIAgentEvent `json:"data"`
	NextCursor int64              `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}
type OpenAIAgentArtifact struct {
	ObjectID    string `json:"object_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Kind        string `json:"kind"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}
type OpenAIAgentArtifactUpload struct {
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
	Destination   string `json:"destination,omitempty"`
}
type OpenAIAgentArtifactExport struct {
	Path string `json:"path"`
}
type OpenAIAgentApproval struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	TurnID      string          `json:"turn_id"`
	CallID      string          `json:"call_id"`
	ToolName    string          `json:"tool_name"`
	Status      string          `json:"status"`
	RequestedBy string          `json:"requested_by"`
	Arguments   json.RawMessage `json:"arguments"`
	RequestedAt string          `json:"requested_at"`
	ExpiresAt   string          `json:"expires_at"`
}
type OpenAIAgentUsage struct {
	Status       string `json:"status"`
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
	TotalTokens  *int64 `json:"total_tokens,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	Source       string `json:"source"`
}
type OpenAIAgentCapabilities struct {
	Enabled              bool     `json:"enabled"`
	Provider             string   `json:"provider"`
	EnvironmentModes     []string `json:"environment_modes"`
	ToolTypes            []string `json:"tool_types"`
	FileOperations       []string `json:"file_operations"`
	ExecutorVersion      string   `json:"executor_version,omitempty"`
	Readiness            string   `json:"readiness"`
	CanManageConnections bool     `json:"can_manage_connections"`
	CanRunSessions       bool     `json:"can_run_sessions"`
	CanResolveApprovals  bool     `json:"can_resolve_approvals"`
}

type OpenAIAgentsResource struct {
	Connections *OpenAIAgentConnectionsResource
	Sessions    *OpenAIAgentSessionsResource
	Approvals   *OpenAIAgentApprovalsResource
}
type OpenAIAgentConnectionsResource struct{ t *Transport }
type OpenAIAgentSessionsResource struct{ t *Transport }
type OpenAIAgentApprovalsResource struct{ t *Transport }

func newOpenAIAgentsResource(t *Transport) *OpenAIAgentsResource {
	return &OpenAIAgentsResource{Connections: &OpenAIAgentConnectionsResource{t: t}, Sessions: &OpenAIAgentSessionsResource{t: t}, Approvals: &OpenAIAgentApprovalsResource{t: t}}
}

func (r *OpenAIAgentsResource) Capabilities(ctx context.Context) (OpenAIAgentCapabilities, error) {
	var out OpenAIAgentCapabilities
	err := openAIRequest(r.Connections.t, ctx, http.MethodGet, openAIAgentsPath+"/capabilities", nil, &out)
	return out, err
}

func resourceID(id string) (string, error) {
	if id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("everstack: resource ID is required")
	}
	return url.PathEscape(id), nil
}
func (r *OpenAIAgentConnectionsResource) List(ctx context.Context) ([]OpenAIAgentConnection, error) {
	var page struct {
		Data []OpenAIAgentConnection `json:"data"`
	}
	err := openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/connections", nil, &page)
	return page.Data, err
}
func (r *OpenAIAgentConnectionsResource) Get(ctx context.Context, id string) (OpenAIAgentConnection, error) {
	var out OpenAIAgentConnection
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/connections/"+v, nil, &out)
	return out, e
}
func (r *OpenAIAgentConnectionsResource) Create(ctx context.Context, p OpenAIAgentConnectionParams) (OpenAIAgentConnection, error) {
	var out OpenAIAgentConnection
	e := openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/connections", p, &out)
	return out, e
}
func (r *OpenAIAgentConnectionsResource) Update(ctx context.Context, id string, p OpenAIAgentConnectionUpdate) (OpenAIAgentConnection, error) {
	var out OpenAIAgentConnection
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodPatch, openAIAgentsPath+"/connections/"+v, p, &out)
	return out, e
}
func (r *OpenAIAgentConnectionsResource) Delete(ctx context.Context, id string) error {
	v, e := resourceID(id)
	if e != nil {
		return e
	}
	return openAIRequest(r.t, ctx, http.MethodDelete, openAIAgentsPath+"/connections/"+v, nil, nil)
}

func (r *OpenAIAgentSessionsResource) List(ctx context.Context) ([]OpenAIAgentSession, error) {
	var page struct {
		Data []OpenAIAgentSession `json:"data"`
	}
	err := openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/sessions", nil, &page)
	return page.Data, err
}
func (r *OpenAIAgentSessionsResource) Get(ctx context.Context, id string) (OpenAIAgentSession, error) {
	var out OpenAIAgentSession
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/sessions/"+v, nil, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Create(ctx context.Context, p OpenAIAgentSessionParams) (OpenAIAgentSession, error) {
	var out OpenAIAgentSession
	e := openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/sessions", p, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Input(ctx context.Context, id string, p OpenAIAgentInputParams) (OpenAIAgentAccepted, error) {
	var out OpenAIAgentAccepted
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/sessions/"+v+"/input", p, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Cancel(ctx context.Context, id string) (OpenAIAgentAccepted, error) {
	var out OpenAIAgentAccepted
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/sessions/"+v+"/cancel", nil, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Delete(ctx context.Context, id string) (OpenAIAgentDeleted, error) {
	var out OpenAIAgentDeleted
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodDelete, openAIAgentsPath+"/sessions/"+v, nil, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Events(ctx context.Context, id string, after int64, limit int) (OpenAIAgentEventPage, error) {
	var out OpenAIAgentEventPage
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	q := url.Values{}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := openAIAgentsPath + "/sessions/" + v + "/events"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	e = openAIRequest(r.t, ctx, http.MethodGet, path, nil, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Artifacts(ctx context.Context, id string) ([]OpenAIAgentArtifact, error) {
	var out struct {
		Data []OpenAIAgentArtifact `json:"data"`
	}
	v, e := resourceID(id)
	if e != nil {
		return nil, e
	}
	e = openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/sessions/"+v+"/artifacts", nil, &out)
	return out.Data, e
}
func (r *OpenAIAgentSessionsResource) DownloadArtifact(ctx context.Context, sid, aid string) (OpenAIAgentArtifact, error) {
	var out OpenAIAgentArtifact
	s, e := resourceID(sid)
	if e != nil {
		return out, e
	}
	a, e := resourceID(aid)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/sessions/"+s+"/artifacts/"+a+"/download", nil, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) UploadArtifact(ctx context.Context, sid string, p OpenAIAgentArtifactUpload) (OpenAIAgentArtifact, error) {
	var out OpenAIAgentArtifact
	s, e := resourceID(sid)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/sessions/"+s+"/inputs", p, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) ExportArtifact(ctx context.Context, sid string, p OpenAIAgentArtifactExport) (OpenAIAgentArtifact, error) {
	var out OpenAIAgentArtifact
	s, e := resourceID(sid)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/sessions/"+s+"/artifacts/export", p, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) DeleteArtifact(ctx context.Context, sid, aid string) (OpenAIAgentDeleted, error) {
	var out OpenAIAgentDeleted
	s, e := resourceID(sid)
	if e != nil {
		return out, e
	}
	a, e := resourceID(aid)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodDelete, openAIAgentsPath+"/sessions/"+s+"/artifacts/"+a, nil, &out)
	return out, e
}
func (r *OpenAIAgentSessionsResource) Usage(ctx context.Context, sid string) (OpenAIAgentUsage, error) {
	var out OpenAIAgentUsage
	s, e := resourceID(sid)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/sessions/"+s+"/usage", nil, &out)
	return out, e
}
func (r *OpenAIAgentApprovalsResource) List(ctx context.Context) ([]OpenAIAgentApproval, error) {
	var out struct {
		Data []OpenAIAgentApproval `json:"data"`
	}
	e := openAIRequest(r.t, ctx, http.MethodGet, openAIAgentsPath+"/approvals", nil, &out)
	return out.Data, e
}
func (r *OpenAIAgentApprovalsResource) Resolve(ctx context.Context, id string, decision string) (OpenAIAgentResolved, error) {
	var out OpenAIAgentResolved
	v, e := resourceID(id)
	if e != nil {
		return out, e
	}
	e = openAIRequest(r.t, ctx, http.MethodPost, openAIAgentsPath+"/approvals/"+v+"/resolve", map[string]string{"decision": decision}, &out)
	return out, e
}

// openAIRequest deliberately performs one mutation attempt and rejects all
// redirects because the request carries Everstack credentials.
func openAIRequest(t *Transport, ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		rd = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, t.baseURL+path, rd)
	if e != nil {
		return e
	}
	t.setHeaders(req)
	hc := *t.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := hc.Do(req)
	if e != nil {
		return &ConnectionError{Err: e}
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(resp.Body)
	if e != nil {
		return e
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("everstack: credential-bearing redirect rejected")
	}
	if resp.StatusCode >= 400 {
		return parseAPIError(resp.StatusCode, b)
	}
	if out != nil && len(strings.TrimSpace(string(b))) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}
func openAIRaw(t *Transport, ctx context.Context, path string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+path, nil)
	if e != nil {
		return nil, e
	}
	t.setHeaders(req)
	hc := *t.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := hc.Do(req)
	if e != nil {
		return nil, &ConnectionError{Err: e}
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(resp.Body)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("everstack: credential-bearing redirect rejected")
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, b)
	}
	return b, nil
}
