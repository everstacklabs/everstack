package serverauth

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/adk"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/mcp/server"
	"github.com/everstacklabs/everstack/internal/mcp/serverprovider"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

func TestMCPRestrictedKeyCannotListOrCallADK(t *testing.T) {
	for _, surfaces := range [][]string{nil, {"mcp"}, {"inference", "mcp", "telemetry", "a2a"}} {
		auth := newWithLookup(fakeLookup{result: apikeyq.APIKeyReadModel{ID: "key", OrgID: strptr("tenant"), Surfaces: surfaces}}, nil).ForSurface(apikey.SurfaceMCP)
		handler := server.New(serverprovider.New(serverprovider.Deps{ADK: &adk.Runtime{}}), auth, nil)
		for _, call := range []bool{false, true} {
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
			if call {
				body = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_adk_agent","arguments":{}}}`
			}
			r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer key")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("status %d", w.Code)
			}
			if call {
				expected := "unknown tool: run_adk_agent"
				if surfaces == nil {
					expected = "agent_code is required"
				}
				if !strings.Contains(w.Body.String(), expected) {
					t.Fatalf("call did not preserve restriction=%v: %s", surfaces != nil, w.Body.String())
				}
			} else if strings.Contains(w.Body.String(), "run_adk_agent") != (surfaces == nil) {
				t.Fatal("restricted key saw ADK")
			}
		}
	}
}

func TestHostedADKDepthGapDocumented(t *testing.T) {
	data, err := os.ReadFile("../../../docs/design/personal-bots-gateway-2026-11-09.md")
	if errors.Is(err, os.ErrNotExist) {
		// docs/design is not part of the published tree.
		t.Skip("design doc not present in this tree")
	}
	if err != nil {
		t.Fatal(err)
	}
	section := strings.SplitN(strings.SplitN(string(data), "### Abuse limits", 2)[1], "### Brain", 2)[0]
	for _, text := range []string{"hosted ADK Python runtime", "does not forward the depth", "surface-limited keys cannot call `run_adk_agent`", "known gap for ordinary keys"} {
		if !strings.Contains(section, text) {
			t.Fatalf("missing ADK boundary: %q", text)
		}
	}
}
