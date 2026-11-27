package interop_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/everstacklabs/everstack/internal/interop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/gorilla/mux"
)

type auditRouter struct{ r *mux.Router }

func (a auditRouter) HandleFunc(path string, f http.HandlerFunc) { a.r.HandleFunc(path, f) }

func TestInteropDashboardIdentityRequiredOnEveryRoute(t *testing.T) {
	r := mux.NewRouter()
	interop.NewHandlers(interop.NewStore(nil), false, true).Mount(auditRouter{r})
	handler := httpmw.NewLocalTenantMiddleware(nil, false).Wrap(r)
	contexts := []context.Context{
		context.Background(),
		contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant", "digest", "ordinary"),
		contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant", "digest", "bot"), []string{"inference", "mcp", "telemetry", "a2a"}),
	}
	for _, ctx := range contexts {
		for _, path := range []string{
			"/api/interop/a2a/published", "/api/interop/a2a/published/agent", "/api/interop/mcp/tools", "/api/interop/mcp/tools/run_agent", "/api/interop/remotes", "/api/interop/remotes/id", "/api/interop/adk/status", "/api/interop/runs", "/api/interop/runs/id", "/api/interop/runs/id/cancel",
		} {
			for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
				request := httptest.NewRequest(method, path, strings.NewReader(`{}`)).WithContext(ctx)
				request.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, request)
				if w.Code != 401 || strings.TrimSpace(w.Body.String()) != `{"error":"unauthorized"}` {
					t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
				}
			}
		}
	}
	ctx := contextkeys.WithUserID(contextkeys.WithTenantAuthenticated(context.Background()), "dashboard-user")
	request := httptest.NewRequest("GET", "/api/interop/adk/status", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
}
