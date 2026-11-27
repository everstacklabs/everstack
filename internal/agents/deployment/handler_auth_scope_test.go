package deployment

import (
	"os"
	"strings"
	"testing"
)

// A deployment key is issued for one agent. The handler used to resolve a
// requested version under the agent named in the URL, which let a key for
// agent A invoke agent B in the same tenant. This pins the two lines that
// close it; the store-backed behaviour needs a database to exercise.
func TestAuthenticateResolvesVersionsUnderTheKeysAgent(t *testing.T) {
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	if strings.Contains(code, "GetActiveDeployment(r.Context(), agentID,") {
		t.Fatal("authenticate must not look a version up under the URL's agent id")
	}
	if !strings.Contains(code, "GetActiveDeployment(r.Context(), dep.AgentID, key.TenantID, version)") {
		t.Fatal("authenticate must look the version up under the key's own agent")
	}
	if !strings.Contains(code, "agentID != dep.AgentID") {
		t.Fatal("authenticate must refuse a URL agent that is not the key's agent")
	}
}
