package interop

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// Router is the subset of the API needed to mount routes (satisfied by *api.API).
type Router interface {
	HandleFunc(path string, f http.HandlerFunc)
}

// Handlers serves the interop admin control plane under /v1/interop/*. These are
// first-party (admin UI) endpoints: tenant is resolved from the request context
// (set by the standard admin auth/tenant middleware), and every handler fails
// closed if no tenant is present. There is deliberately NO single-tenant
// fallback - these settings gate externally reachable surfaces.
type Handlers struct {
	store      *Store
	sharedMode bool // true in managed cloud (multi-tenant); false self-hosted
	adkCapable bool // true when the ADK runtime is constructed (sandbox backend present)
}

// NewHandlers builds the admin handlers over the given store. sharedMode reflects
// whether this is a managed cloud instance; adkCapable reflects whether the ADK
// runtime is available on this instance (it is ungated/universal - on wherever a
// sandbox backend exists).
func NewHandlers(store *Store, sharedMode bool, adkCapable bool) *Handlers {
	return &Handlers{store: store, sharedMode: sharedMode, adkCapable: adkCapable}
}

// Mount registers the admin routes.
//
// NOTE: these are deliberately mounted under /api/interop, NOT /v1/interop. The
// grpc-gateway owns a greedy PathPrefix("/v1") subrouter registered during
// api.NewAPI (before these HandleFuncs run); anything under /v1 that isn't a
// gateway-transcoded RPC is swallowed and 404s. /api/* has no such catch-all,
// mirroring how /mcp and /api/v1/storage/* avoid the same collision.
func (h *Handlers) Mount(r Router) {
	r.HandleFunc("/api/interop/a2a/published", h.publishedList)
	r.HandleFunc("/api/interop/a2a/published/{agentId}", h.publishedSet)
	r.HandleFunc("/api/interop/mcp/tools", h.toolSettings)
	r.HandleFunc("/api/interop/mcp/tools/{tool}", h.toolSet)
	r.HandleFunc("/api/interop/remotes", h.remotes)
	r.HandleFunc("/api/interop/remotes/{id}", h.remoteDelete)
	r.HandleFunc("/api/interop/adk/status", h.adkStatus)
	// No adk/enabled route: ADK is a universal capability - not gated, no off-switch.
}

func tenantFrom(r *http.Request) string {
	if t := contextkeys.GetTenantID(r.Context()); t != "" {
		return t
	}
	return contextkeys.ExtractTenantID(r.Context())
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// requireTenant resolves the tenant or writes 401 and returns "".
func requireTenant(w http.ResponseWriter, r *http.Request) string {
	t := tenantFrom(r)
	if t == "" {
		http.Error(w, `{"error":"tenant context required"}`, http.StatusUnauthorized)
	}
	return t
}

// ─── A2A publish ────────────────────────────────────────────────────

func (h *Handlers) publishedList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	m, err := h.store.ListPublishedAgents(r.Context(), tenant)
	if err != nil {
		http.Error(w, `{"error":"failed to list published agents"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"published": m})
}

func (h *Handlers) publishedSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	agentID := mux.Vars(r)["agentId"]
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.SetAgentPublished(r.Context(), tenant, agentID, body.Enabled); err != nil {
		http.Error(w, `{"error":"failed to update"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"agent_id": agentID, "enabled": body.Enabled})
}

// ─── MCP tool settings ──────────────────────────────────────────────

func (h *Handlers) toolSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	m, err := h.store.ToolSettings(r.Context(), tenant)
	if err != nil {
		http.Error(w, `{"error":"failed to list tool settings"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tools": m})
}

func (h *Handlers) toolSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	tool := mux.Vars(r)["tool"]
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.SetToolEnabled(r.Context(), tenant, tool, body.Enabled); err != nil {
		http.Error(w, `{"error":"failed to update"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tool": tool, "enabled": body.Enabled})
}

// ─── Remote agents ──────────────────────────────────────────────────

func (h *Handlers) remotes(w http.ResponseWriter, r *http.Request) {
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := h.store.ListRemoteAgents(r.Context(), tenant)
		if err != nil {
			http.Error(w, `{"error":"failed to list remotes"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"remotes": list})
	case http.MethodPost:
		var body struct {
			Name      string `json:"name"`
			Endpoint  string `json:"endpoint"`
			AuthToken string `json:"auth_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Endpoint) == "" {
			http.Error(w, `{"error":"name and endpoint are required"}`, http.StatusBadRequest)
			return
		}
		if err := h.store.UpsertRemoteAgent(r.Context(), tenant, body.Name, body.Endpoint, body.AuthToken); err != nil {
			http.Error(w, `{"error":"failed to save remote"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"name": body.Name, "endpoint": body.Endpoint})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) remoteDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	id := mux.Vars(r)["id"]
	if err := h.store.DeleteRemoteAgent(r.Context(), tenant, id); err != nil {
		http.Error(w, `{"error":"failed to delete remote"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": id})
}

// ─── ADK runtime status (read-only) ─────────────────────────────────

// adkStatus reports whether the ADK runtime is available on this instance. ADK
// is a universal capability - on wherever a sandbox backend exists, with no
// per-tenant gate and no off-switch - so "capable" and "enabled" are the same
// thing here. The network mode is reported for transparency (egress policy).
func (h *Handlers) adkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	networkMode := os.Getenv("EVS_ADK_NETWORK_MODE")
	if h.sharedMode && networkMode == "" {
		networkMode = "whitelist" // cloud default (mirrors the runtime wiring)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":          h.adkCapable,
		"instance_capable": h.adkCapable,
		"tenant_enabled":   h.adkCapable,
		"shared_mode":      h.sharedMode,
		"network_mode":     networkMode,
	})
}
