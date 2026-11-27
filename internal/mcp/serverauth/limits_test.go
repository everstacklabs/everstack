package serverauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	a2a "github.com/everstacklabs/everstack/internal/a2a/server"
	"github.com/everstacklabs/everstack/internal/api/http/otlp"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/mcp/server"
	"github.com/everstacklabs/everstack/internal/query"
	keyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	"github.com/gorilla/mux"
)

type limitsLookup map[string]keyq.APIKeyReadModel

func (m limitsLookup) Handle(_ context.Context, q query.Query) (interface{}, error) {
	v, ok := m[q.(*keyq.GetApiKeyByHashQuery).Hash]
	if !ok {
		return nil, nil
	}
	return v, nil
}

type limitTools struct{}

func (limitTools) ListTools(context.Context, string) ([]mcp.ToolInfo, error) { return nil, nil }
func (limitTools) CallTool(context.Context, string, string, map[string]interface{}) (*mcp.ToolCallResult, error) {
	return &mcp.ToolCallResult{}, nil
}

type limitAgents struct{}

func (limitAgents) GetAgent(context.Context, string, string) (string, string, bool, error) {
	return "agent", "", true, nil
}

type limitRunner struct{}

func (limitRunner) Run(context.Context, string, string, string) (string, error) { return "done", nil }

type limitRouter struct{ *mux.Router }

func (r limitRouter) HandleFunc(p string, h http.HandlerFunc) { r.Router.HandleFunc(p, h) }

type countRecorder struct{ calls int }

func (r *countRecorder) Record(string, string, apikey.Surface) { r.calls++ }

func limitAuth(t *testing.T, rpm *int, restricted bool) *APIKeyAuthenticator {
	t.Helper()
	lookup := limitsLookup{}
	for _, key := range []string{"one", "two"} {
		hash, ok := apikey.HashFromContext(context.Background(), key)
		if !ok {
			t.Fatal("hash")
		}
		tenant := "tenant"
		rm := keyq.APIKeyReadModel{ID: key, OrgID: &tenant, RateLimitRPM: rpm}
		if restricted {
			rm.Surfaces = []string{"inference", "mcp", "telemetry", "a2a"}
		}
		lookup[hash] = rm
	}
	return newWithLookup(lookup, nil)
}
func surfaceHandler(a *APIKeyAuthenticator, s apikey.Surface) http.Handler {
	switch s {
	case apikey.SurfaceMCP:
		return server.New(limitTools{}, a.ForSurface(s), nil)
	case apikey.SurfaceTelemetry:
		return otlp.WithTenantAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), a.ForSurface(s))
	default:
		r := mux.NewRouter()
		a2a.New(a2a.Deps{Agents: limitAgents{}, Runner: limitRunner{}, Auth: a.ForSurface(s)}).Mount(limitRouter{r})
		return r
	}
}
func surfaceCall(h http.Handler, s apikey.Surface, key string) *httptest.ResponseRecorder {
	path, body := "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if s == apikey.SurfaceTelemetry {
		path = "/v1/traces"
	}
	if s == apikey.SurfaceA2A {
		path = "/a2a/agents/agent"
		body = `{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"parts":[{"kind":"text","text":"hi"}]}}}`
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestSurfaceDefaultsAndDisable(t *testing.T) {
	for _, tc := range []struct {
		s   apikey.Surface
		env string
	}{{apikey.SurfaceMCP, "EVS_MCP_RATE_LIMIT_RPM"}, {apikey.SurfaceTelemetry, "EVS_OTLP_RATE_LIMIT_RPM"}, {apikey.SurfaceA2A, "EVS_A2A_RATE_LIMIT_RPM"}} {
		t.Run(string(tc.s), func(t *testing.T) {
			for _, disabled := range []bool{false, true} {
				value := "1"
				if disabled {
					value = "0"
				}
				t.Setenv(tc.env, value)
				previous := apikey.SetRuntime(apikey.NewRuntime(authratelimit.NewMemoryStore(), nil))
				// The defaults are for bot keys. An ordinary key keeps the
				// unlimited behaviour it had before bots existed.
				ordinary := surfaceHandler(limitAuth(t, nil, false), tc.s)
				for i := 0; i < 3; i++ {
					if w := surfaceCall(ordinary, tc.s, "one"); w.Code != 200 {
						t.Fatalf("ordinary key limited by a bot default: %d", w.Code)
					}
				}
				apikey.SetRuntime(apikey.NewRuntime(authratelimit.NewMemoryStore(), nil))
				h := surfaceHandler(limitAuth(t, nil, true), tc.s)
				first := surfaceCall(h, tc.s, "one")
				second := surfaceCall(h, tc.s, "one")
				if first.Code != 200 {
					t.Fatalf("under: %d %s", first.Code, first.Body)
				}
				if disabled {
					if second.Code != 200 {
						t.Fatalf("disabled: %d", second.Code)
					}
				} else if second.Code != 429 || second.Header().Get("Retry-After") == "" {
					t.Fatalf("over: %d %v", second.Code, second.Header())
				}
				if other := surfaceCall(h, tc.s, "two"); other.Code != 200 {
					t.Fatalf("different key shares bucket: %d", other.Code)
				}
				apikey.SetRuntime(previous)
			}
		})
	}
}
func TestBotBudgetSpansSurfaceViews(t *testing.T) {
	for _, env := range []string{"EVS_MCP_RATE_LIMIT_RPM", "EVS_OTLP_RATE_LIMIT_RPM", "EVS_A2A_RATE_LIMIT_RPM"} {
		t.Setenv(env, "0")
	}
	calls := &countRecorder{}
	previous := apikey.SetRuntime(apikey.NewRuntime(authratelimit.NewMemoryStore(), calls))
	defer apikey.SetRuntime(previous)
	rpm := 1
	a := limitAuth(t, &rpm, true)
	if w := surfaceCall(surfaceHandler(a, apikey.SurfaceMCP), apikey.SurfaceMCP, "one"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, s := range []apikey.Surface{apikey.SurfaceTelemetry, apikey.SurfaceA2A} {
		if w := surfaceCall(surfaceHandler(a, s), s, "one"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Fatalf("shared budget: %s %d", s, w.Code)
		}
	}
	if calls.calls != 1 {
		t.Fatalf("recorded denied calls: %d", calls.calls)
	}
}

type unavailableStore struct{}

func (unavailableStore) Consume(context.Context, string, authratelimit.Rule) (authratelimit.Decision, error) {
	return authratelimit.Decision{}, errors.New("offline")
}
func (unavailableStore) Peek(context.Context, string) (authratelimit.Window, error) {
	return authratelimit.Window{}, errors.New("offline")
}
func TestSurfaceLimiterFailsOpen(t *testing.T) {
	previous := apikey.SetRuntime(apikey.NewRuntime(unavailableStore{}, nil))
	defer apikey.SetRuntime(previous)
	for _, s := range []apikey.Surface{apikey.SurfaceMCP, apikey.SurfaceTelemetry, apikey.SurfaceA2A} {
		if w := surfaceCall(surfaceHandler(limitAuth(t, nil, false), s), s, "one"); w.Code != 200 {
			t.Fatalf("%s: %d", s, w.Code)
		}
	}
}
