// Package serverprovider implements the tenant-scoped ToolProvider for the MCP
// server (internal/mcp/server). It exposes a curated, tenant-safe slice of
// Everstack's capabilities to external MCP clients (Claude Desktop, Cursor,
// Google ADK, ...).
//
// Catalog (each entry appears only when its backend dependency is wired):
//   - builtins:  everstack_whoami, everstack_echo (zero-dep, prove the loop)
//   - memory:    memory_query, memory_store     (vector memory backend)
//   - web:       web_search, web_fetch          (SearXNG / Jina / local)
//   - agents:    list_agents, get_agent, run_agent
//
// run_agent is the headline: it lets any MCP client invoke a *deployed*
// Everstack agent and get its output — i.e. use an Everstack agent as a tool.
//
// Deliberately excluded for now: sandbox exec/destroy. Those key on a sessionID
// with manual per-call ownership gating; exposing untested cross-tenant-capable
// code execution on an externally reachable endpoint is the tenant-isolation
// hazard we refuse to ship unverified. They land once exercised end to end.
package serverprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jmoiron/sqlx"

	adk "github.com/everstacklabs/everstack/internal/adk"
	"github.com/everstacklabs/everstack/internal/agents/agentrun"
	tools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/mcp/server"
	"github.com/everstacklabs/everstack/internal/memory"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
)

var errNoTenant = errors.New("mcp server: tenant id is required")

// Deps are the optional backends the provider exposes as tools. Any nil/zero
// dependency simply omits the corresponding tools — the server still serves
// whatever is wired.
type Deps struct {
	// Memory
	MemStore    memory.VectorStore
	MemEmbedder memory.EmbedderInterface
	MemModel    string
	MemDim      int

	// Web (web_fetch needs only HTTPClient; web_search also needs a SearXNG URL)
	HTTPClient   *http.Client
	WebSearchURL string // SearXNG base URL; empty disables web_search
	JinaAPIKey   string

	// Agents
	AgentsDB *sqlx.DB         // for list_agents / get_agent (tenant-scoped queries)
	Runner   *agentrun.Runner // for run_agent (invokes deployed agents)

	// ADK runtime (run an ADK agent in a tenant-scoped sandbox)
	ADK *adk.Runtime

	// ToolSettings optionally resolves per-tenant tool enable/disable overrides.
	// Tools without an explicit override default to enabled.
	ToolSettings ToolSettingsResolver

	// ADKEnabled optionally gates run_adk_agent per tenant. When nil (e.g.
	// self-hosted single-tenant), the tool follows the instance capability
	// (ADK != nil) alone. When set (cloud), the tenant must also have opted in —
	// run_adk_agent runs arbitrary code, so it is never self-enabled by default.
	ADKEnabled ADKCapabilityResolver
}

// ToolSettingsResolver returns per-tenant tool enable/disable overrides
// (tool name -> enabled). Satisfied by *interop.Store.
type ToolSettingsResolver interface {
	ToolSettings(ctx context.Context, tenantID string) (map[string]bool, error)
}

// ADKCapabilityResolver reports whether a tenant has opted into the ADK runtime.
// Satisfied by *interop.Store.
type ADKCapabilityResolver interface {
	IsADKEnabled(ctx context.Context, tenantID string) (bool, error)
}

const toolRunADKAgent = "run_adk_agent"

// adkAllowed reports whether run_adk_agent may be exposed/called for the tenant.
func (p *Provider) adkAllowed(ctx context.Context, tenantID string) bool {
	if p.d.ADK == nil {
		return false // instance not capable
	}
	if p.d.ADKEnabled == nil {
		return true // no per-tenant gate (self-hosted): instance capability is enough
	}
	ok, err := p.d.ADKEnabled.IsADKEnabled(ctx, tenantID)
	return err == nil && ok
}

// Provider is a tenant-scoped MCP ToolProvider.
type Provider struct {
	d Deps
}

var _ server.ToolProvider = (*Provider)(nil)

// New constructs a Provider from the given dependencies.
func New(d Deps) *Provider { return &Provider{d: d} }

// buildHandlers constructs the synthetic tool handlers for a tenant. Each
// handler is bound to tenantID so a call can only touch that tenant's data —
// the per-request isolation boundary.
func (p *Provider) buildHandlers(tenantID string) []tools.SyntheticToolHandler {
	hs := []tools.SyntheticToolHandler{
		&whoamiTool{tenantID: tenantID},
		&echoTool{},
	}

	if p.d.MemStore != nil && p.d.MemEmbedder != nil {
		hs = append(hs,
			&tools.MemoryQueryHandler{Store: p.d.MemStore, Embedder: p.d.MemEmbedder, TenantID: tenantID},
			&tools.MemoryStoreHandler{
				Store:                     p.d.MemStore,
				Embedder:                  p.d.MemEmbedder,
				TenantID:                  tenantID,
				DefaultEmbeddingModel:     p.d.MemModel,
				DefaultEmbeddingDimension: p.d.MemDim,
			},
		)
	}

	if p.d.HTTPClient != nil {
		if p.d.WebSearchURL != "" {
			hs = append(hs, &tools.WebSearchHandler{SearXNGURL: p.d.WebSearchURL, HTTPClient: p.d.HTTPClient})
		}
		// web_fetch omits HTTPClient so it uses the SSRF-guarded client — it
		// fetches arbitrary agent-supplied URLs and must not reach internal hosts.
		hs = append(hs, &tools.WebFetchHandler{JinaAPIKey: p.d.JinaAPIKey})
	}

	if p.d.AgentsDB != nil {
		hs = append(hs,
			&listAgentsTool{db: p.d.AgentsDB, tenantID: tenantID},
			&getAgentTool{db: p.d.AgentsDB, tenantID: tenantID},
		)
	}
	if p.d.Runner != nil {
		hs = append(hs, &runAgentTool{runner: p.d.Runner, tenantID: tenantID})
	}
	if p.d.ADK != nil {
		hs = append(hs, &runADKTool{rt: p.d.ADK, tenantID: tenantID})
	}
	return hs
}

// resolveSettings returns the tenant's per-tool enable/disable overrides, or nil
// if no resolver is configured or the lookup fails (fail-open to "all enabled").
func (p *Provider) resolveSettings(ctx context.Context, tenantID string) map[string]bool {
	if p.d.ToolSettings == nil {
		return nil
	}
	m, err := p.d.ToolSettings.ToolSettings(ctx, tenantID)
	if err != nil {
		return nil
	}
	return m
}

// ListTools implements server.ToolProvider.
func (p *Provider) ListTools(ctx context.Context, tenantID string) ([]mcp.ToolInfo, error) {
	if tenantID == "" {
		return nil, errNoTenant
	}
	settings := p.resolveSettings(ctx, tenantID)
	handlers := p.buildHandlers(tenantID)
	out := make([]mcp.ToolInfo, 0, len(handlers))
	for _, h := range handlers {
		if en, ok := settings[h.Name()]; ok && !en {
			continue // explicitly disabled for this tenant
		}
		if h.Name() == toolRunADKAgent && !p.adkAllowed(ctx, tenantID) {
			continue // ADK not enabled for this tenant
		}
		def := h.Definition()
		schema := def.Function.Parameters
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, mcp.ToolInfo{
			Name:        def.Function.Name,
			Description: def.Function.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

// CallTool implements server.ToolProvider.
func (p *Provider) CallTool(ctx context.Context, tenantID, name string, args map[string]interface{}) (*mcp.ToolCallResult, error) {
	if tenantID == "" {
		return nil, errNoTenant
	}
	settings := p.resolveSettings(ctx, tenantID)
	for _, h := range p.buildHandlers(tenantID) {
		if h.Name() != name {
			continue
		}
		if en, ok := settings[name]; ok && !en {
			return nil, fmt.Errorf("tool %q is disabled for this tenant", name)
		}
		if name == toolRunADKAgent && !p.adkAllowed(ctx, tenantID) {
			return nil, fmt.Errorf("the ADK runtime is not enabled for this tenant")
		}
		out, err := h.Execute(ctx, args)
		if err != nil {
			return nil, err
		}
		return &mcp.ToolCallResult{Content: []mcp.ContentBlock{{Type: "text", Text: out}}}, nil
	}
	return nil, fmt.Errorf("unknown tool: %s", name)
}

// objectSchema is a small helper for a JSON-Schema object with the given props.
func objectSchema(props map[string]interface{}, required ...string) map[string]interface{} {
	if props == nil {
		props = map[string]interface{}{}
	}
	s := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// ─── builtin tools ──────────────────────────────────────────────────

type whoamiTool struct{ tenantID string }

func (t *whoamiTool) Name() string { return "everstack_whoami" }

func (t *whoamiTool) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{
		Name:        "everstack_whoami",
		Description: "Return the Everstack tenant this MCP connection is authenticated as, plus server version. Useful to verify connectivity and identity.",
		Parameters:  objectSchema(nil),
	}}
}

func (t *whoamiTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	b, _ := json.Marshal(map[string]string{"tenant_id": t.tenantID, "server": "everstack-mcp-server", "version": "1.0.0"})
	return string(b), nil
}

type echoTool struct{}

func (t *echoTool) Name() string { return "everstack_echo" }

func (t *echoTool) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{
		Name:        "everstack_echo",
		Description: "Echo back the provided message. A simple connectivity test.",
		Parameters: objectSchema(map[string]interface{}{
			"message": map[string]interface{}{"type": "string", "description": "The message to echo back."},
		}, "message"),
	}}
}

func (t *echoTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	msg, _ := args["message"].(string)
	return msg, nil
}

// ─── agent tools ────────────────────────────────────────────────────

// agentSummary is the compact view returned by list_agents / get_agent.
type agentSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	Enabled     bool   `json:"enabled"`
}

func summarize(m agentsquery.AgentDefinitionReadModel) agentSummary {
	s := agentSummary{ID: m.ID, Name: m.Name, Model: m.Model, Enabled: m.Enabled}
	if m.Description.Valid {
		s.Description = m.Description.String
	}
	return s
}

type listAgentsTool struct {
	db       *sqlx.DB
	tenantID string
}

func (t *listAgentsTool) Name() string { return "list_agents" }

func (t *listAgentsTool) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{
		Name:        "list_agents",
		Description: "List the Everstack agents available in this tenant (id, name, description, model). Use run_agent to invoke one.",
		Parameters:  objectSchema(nil),
	}}
}

func (t *listAgentsTool) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	enabled := true
	q := agentsquery.NewListAgentsQuery(t.tenantID, &enabled, false, nil, nil, 200, 0)
	res, err := agentsquery.NewListAgentsQueryHandler(t.db).Handle(ctx, q)
	if err != nil {
		return "", fmt.Errorf("list agents failed: %w", err)
	}
	models, _ := res.([]agentsquery.AgentDefinitionReadModel)
	out := make([]agentSummary, 0, len(models))
	for _, m := range models {
		out = append(out, summarize(m))
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

type getAgentTool struct {
	db       *sqlx.DB
	tenantID string
}

func (t *getAgentTool) Name() string { return "get_agent" }

func (t *getAgentTool) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{
		Name:        "get_agent",
		Description: "Get details for a single Everstack agent in this tenant by id.",
		Parameters: objectSchema(map[string]interface{}{
			"agent_id": map[string]interface{}{"type": "string", "description": "The agent id."},
		}, "agent_id"),
	}}
}

func (t *getAgentTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}
	q := agentsquery.NewGetAgentByIDQuery(agentID, t.tenantID)
	res, err := agentsquery.NewAgentByIDQueryHandler(t.db).Handle(ctx, q)
	if err != nil {
		return "", fmt.Errorf("get agent failed: %w", err)
	}
	m, ok := res.(*agentsquery.AgentDefinitionReadModel)
	if !ok || m == nil {
		return fmt.Sprintf("agent %q not found in this tenant", agentID), nil
	}
	b, _ := json.Marshal(summarize(*m))
	return string(b), nil
}

type runAgentTool struct {
	runner   *agentrun.Runner
	tenantID string
}

func (t *runAgentTool) Name() string { return "run_agent" }

func (t *runAgentTool) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{
		Name:        "run_agent",
		Description: "Invoke a deployed Everstack agent by id with a message and return its final response. Only agents with an active deployment are callable.",
		Parameters: objectSchema(map[string]interface{}{
			"agent_id": map[string]interface{}{"type": "string", "description": "The agent id to invoke."},
			"message":  map[string]interface{}{"type": "string", "description": "The user message to send to the agent."},
		}, "agent_id", "message"),
	}}
}

func (t *runAgentTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	message, _ := args["message"].(string)
	if agentID == "" || message == "" {
		return "", fmt.Errorf("agent_id and message are required")
	}
	return t.runner.Run(ctx, t.tenantID, agentID, message)
}

// ─── ADK runtime tool ───────────────────────────────────────────────

type runADKTool struct {
	rt       *adk.Runtime
	tenantID string
}

func (t *runADKTool) Name() string { return "run_adk_agent" }

func (t *runADKTool) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{
		Name:        "run_adk_agent",
		Description: "Run a Google ADK agent on Everstack. Provide Python that defines `root_agent` (an ADK agent) plus an input message; Everstack provisions an isolated sandbox, installs google-adk, runs the agent, and returns its output.",
		Parameters: objectSchema(map[string]interface{}{
			"agent_code": map[string]interface{}{"type": "string", "description": "Python source defining `root_agent` (a google.adk agent)."},
			"input":      map[string]interface{}{"type": "string", "description": "The user message to send the agent."},
			"packages":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional extra pip packages to install."},
		}, "agent_code", "input"),
	}}
}

func (t *runADKTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	code, _ := args["agent_code"].(string)
	input, _ := args["input"].(string)
	if code == "" {
		return "", fmt.Errorf("agent_code is required")
	}
	var pkgs []string
	if raw, ok := args["packages"].([]interface{}); ok {
		for _, x := range raw {
			if s, ok := x.(string); ok {
				pkgs = append(pkgs, s)
			}
		}
	}
	res, err := t.rt.Run(ctx, adk.RunRequest{TenantID: t.tenantID, AgentCode: code, Input: input, Packages: pkgs})
	if err != nil {
		if res != nil && res.Logs != "" {
			return "", fmt.Errorf("%w; logs:\n%s", err, res.Logs)
		}
		return "", err
	}
	return res.Output, nil
}
