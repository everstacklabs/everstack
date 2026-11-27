package bots

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/interop"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/gorilla/mux"
)

type Router interface {
	HandleFunc(string, http.HandlerFunc)
}
type KeyService interface {
	CreateBotKey(context.Context, string, []string) (id, plaintext, hint string, err error)
	RevokeBotKey(context.Context, string) error
}
type RemoteLookup interface {
	ListRemoteAgents(context.Context, string) ([]interop.RemoteAgent, error)
}
type Handlers struct {
	store   Repository
	keys    KeyService
	remotes RemoteLookup
}

func NewHandlers(store Repository, keys KeyService, remotes RemoteLookup) *Handlers {
	return &Handlers{store: store, keys: keys, remotes: remotes}
}
func (h *Handlers) Mount(r Router) {
	r.HandleFunc("/api/bots", h.guard(h.collection))
	r.HandleFunc("/api/bots/{id}", h.guard(h.item))
	r.HandleFunc("/api/bots/{id}/rotate-key", h.guard(h.rotate))
}

func (h *Handlers) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// Only a verified dashboard user may manage credentials. Host selection,
		// local tenant fallback and API-key authentication never qualify.
		if !contextkeys.IsDashboardUser(ctx) {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		tenant := contextkeys.GetTenantID(ctx)
		if tenant == "" {
			tenant = contextkeys.ExtractTenantID(ctx)
		}
		if tenant == "" {
			fail(w, http.StatusUnauthorized, "tenant_required")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				fail(w, http.StatusUnsupportedMediaType, "json_required")
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r.WithContext(contextkeys.WithTenantID(ctx, tenant)))
	}
}
func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
func replyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNameTaken):
		fail(w, http.StatusConflict, "name_taken")
	case errors.Is(err, ErrNotFound):
		fail(w, http.StatusNotFound, "not_found")
	default:
		fail(w, http.StatusInternalServerError, "internal_error")
	}
}
func readBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid_body")
		return false
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		fail(w, http.StatusBadRequest, "invalid_body")
		return false
	}
	return true
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)

func validName(name string) bool {
	return len(name) >= 1 && len(name) <= 64 && namePattern.MatchString(name)
}
func kindSurfaces(kind string) ([]string, bool) {
	switch kind {
	case "grok-bot", "meta-muse":
		return []string{"mcp"}, true
	case "grok-build":
		return []string{"inference", "mcp"}, true
	case "openclaw", "hermes", "nanobot", "zeroclaw", "nanoclaw", "copilotkit-openbot", "nightly-openbot", "custom":
		return []string{"inference", "mcp", "telemetry", "a2a"}, true
	}
	return nil, false
}
func parseSurfaces(raw json.RawMessage, kind string) ([]string, bool) {
	allowed, ok := kindSurfaces(kind)
	if !ok {
		return nil, false
	}
	if len(raw) == 0 {
		if len(allowed) <= 2 {
			return allowed, true
		}
		return []string{"inference", "mcp", "telemetry"}, true
	}
	var surfaces []string
	if json.Unmarshal(raw, &surfaces) != nil || surfaces == nil {
		return nil, false
	}
	seen := map[string]bool{}
	for _, s := range surfaces {
		if !apikey.ValidSurface(apikey.Surface(s)) || seen[s] {
			return nil, false
		}
		seen[s] = true
		found := false
		for _, a := range allowed {
			if a == s {
				found = true
			}
		}
		if !found {
			return nil, false
		}
	}
	return surfaces, true
}
func (h *Handlers) validRemote(w http.ResponseWriter, r *http.Request, name *string) bool {
	if name == nil || *name == "" {
		return true
	}
	remotes, err := h.remotes.ListRemoteAgents(r.Context(), contextkeys.GetTenantID(r.Context()))
	if err != nil {
		replyError(w, err)
		return false
	}
	for _, remote := range remotes {
		if remote.Name == *name {
			return true
		}
	}
	fail(w, http.StatusBadRequest, "remote_not_found")
	return false
}
func nullable(s *string) *string {
	if s != nil && *s == "" {
		return nil
	}
	return s
}
func (h *Handlers) cleanupKey(ctx context.Context, id string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return h.keys.RevokeBotKey(cleanup, id)
}
func (h *Handlers) collection(w http.ResponseWriter, r *http.Request) {
	tenant := contextkeys.GetTenantID(r.Context())
	switch r.Method {
	case http.MethodGet:
		list, err := h.store.List(r.Context(), tenant)
		if err != nil {
			replyError(w, err)
			return
		}
		if list == nil {
			list = []Bot{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"bots": list})
	case http.MethodPost:
		var body struct {
			Name         string          `json:"name"`
			Kind         string          `json:"kind"`
			Surfaces     json.RawMessage `json:"surfaces"`
			RemoteAgent  *string         `json:"remote_agent"`
			RateLimitRPM json.RawMessage `json:"rate_limit_rpm"`
		}
		if !readBody(w, r, &body) {
			return
		}
		rpm, ok := parseRateLimit(body.RateLimitRPM)
		if !ok || string(body.RateLimitRPM) == "null" {
			fail(w, 400, "invalid_rate_limit")
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if !validName(body.Name) {
			fail(w, 400, "invalid_name")
			return
		}
		if _, ok := kindSurfaces(body.Kind); !ok {
			fail(w, 400, "invalid_kind")
			return
		}
		surfaces, ok := parseSurfaces(body.Surfaces, body.Kind)
		if !ok {
			fail(w, 400, "invalid_surfaces")
			return
		}
		if !h.validRemote(w, r, body.RemoteAgent) {
			return
		}
		id, plain, hint, err := h.keys.CreateBotKey(r.Context(), body.Name, surfaces)
		if err != nil {
			replyError(w, err)
			return
		}
		user := contextkeys.GetAuthenticatedUserID(r.Context())
		bot := Bot{RateLimitRPM: rpm, Usage: emptyUsage(), TenantID: tenant, Name: body.Name, Kind: body.Kind, APIKeyID: id, KeyHint: hint, Surfaces: surfaces, RemoteAgent: nullable(body.RemoteAgent), CreatedBy: &user}
		if err = h.store.Insert(r.Context(), &bot); err != nil {
			replyError(w, errors.Join(err, h.cleanupKey(r.Context(), id)))
			return
		}
		apikey.InvalidateCaches("", id)
		writeJSON(w, http.StatusCreated, map[string]interface{}{"bot": bot, "api_key": plain})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h *Handlers) lockedBot(w http.ResponseWriter, r *http.Request) (*Bot, func()) {
	tenant := contextkeys.GetTenantID(r.Context())
	id := mux.Vars(r)["id"]
	unlock, err := h.store.Lock(r.Context(), tenant, id)
	if err != nil {
		replyError(w, err)
		return nil, func() {}
	}
	bot, err := h.store.Get(r.Context(), tenant, id)
	if err != nil {
		unlock()
		replyError(w, err)
		return nil, func() {}
	}
	return bot, unlock
}
func (h *Handlers) item(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	bot, unlock := h.lockedBot(w, r)
	defer unlock()
	if bot == nil {
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := h.keys.RevokeBotKey(r.Context(), bot.APIKeyID); err != nil {
			replyError(w, err)
			return
		}
		apikey.InvalidateCaches("", bot.APIKeyID)
		if err := h.store.Delete(r.Context(), bot.TenantID, bot.ID, bot.UpdatedAt); err != nil {
			replyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		var body struct {
			Name         *string         `json:"name"`
			Surfaces     json.RawMessage `json:"surfaces"`
			RemoteAgent  json.RawMessage `json:"remote_agent"`
			RateLimitRPM json.RawMessage `json:"rate_limit_rpm"`
		}
		if !readBody(w, r, &body) {
			return
		}
		if len(body.RateLimitRPM) > 0 {
			rpm, ok := parseRateLimit(body.RateLimitRPM)
			if !ok {
				fail(w, 400, "invalid_rate_limit")
				return
			}
			bot.RateLimitRPM = rpm
		}
		if body.Name != nil {
			bot.Name = strings.TrimSpace(*body.Name)
			if !validName(bot.Name) {
				fail(w, 400, "invalid_name")
				return
			}
		}
		if len(body.Surfaces) > 0 {
			surfaces, ok := parseSurfaces(body.Surfaces, bot.Kind)
			if !ok {
				fail(w, 400, "invalid_surfaces")
				return
			}
			bot.Surfaces = surfaces
		}
		if len(body.RemoteAgent) != 0 {
			var remote *string
			if json.Unmarshal(body.RemoteAgent, &remote) != nil {
				fail(w, 400, "invalid_body")
				return
			}
			if !h.validRemote(w, r, remote) {
				return
			}
			bot.RemoteAgent = nullable(remote)
		}
		if err := h.store.Update(r.Context(), bot); err != nil {
			replyError(w, err)
			return
		}
		apikey.InvalidateCaches("", bot.APIKeyID)
		writeJSON(w, 200, map[string]interface{}{"bot": bot})
	}
}
func (h *Handlers) rotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	bot, unlock := h.lockedBot(w, r)
	defer unlock()
	if bot == nil {
		return
	}
	id, plain, hint, err := h.keys.CreateBotKey(r.Context(), bot.Name, bot.Surfaces)
	if err != nil {
		replyError(w, err)
		return
	}
	// Revoke before replacing: any failure leaves a deletable bot with a dead
	// old key, rather than an untracked live credential.
	oldKeyID := bot.APIKeyID
	if err = h.keys.RevokeBotKey(r.Context(), bot.APIKeyID); err != nil {
		replyError(w, errors.Join(err, h.cleanupKey(r.Context(), id)))
		return
	}
	apikey.InvalidateCaches("", oldKeyID)
	bot.APIKeyID = id
	bot.KeyHint = hint
	bot.LastSeenAt = nil
	bot.Usage = emptyUsage()
	if err = h.store.Update(r.Context(), bot); err != nil {
		replyError(w, errors.Join(err, h.cleanupKey(r.Context(), id)))
		return
	}
	apikey.InvalidateCaches("", id)
	writeJSON(w, 200, map[string]interface{}{"bot": bot, "api_key": plain})
}

func parseRateLimit(raw json.RawMessage) (*int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var n int
	if json.Unmarshal(raw, &n) != nil || n < 1 || n > 100000 {
		return nil, false
	}
	return &n, true
}
