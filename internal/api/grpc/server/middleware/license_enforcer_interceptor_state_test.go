package middleware

import (
	"context"
	"testing"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
)

// Pins the Connect-side D6/D9 semantics: unlicensed/expired states never wall
// procedures; only an explicitly disabled license blocks write procedures.

func newConnectStateInterceptor() *LicenseConnectInterceptor {
	return NewLicenseConnectInterceptor(nil, apilic.NewDefaultPolicy())
}

func TestConnectUnlicensedPasses(t *testing.T) {
	i := newConnectStateInterceptor()
	for _, proc := range []string{
		"/everstack.agents.v1.AgentsService/ListAgents",
		"/everstack.agents.v1.AgentsService/CreateAgent",
	} {
		if err := i.enforceStateWithTrialFallback(context.Background(), nil, proc); err != nil {
			t.Fatalf("unlicensed %s must pass (D9), got: %v", proc, err)
		}
	}
}

func TestConnectInactiveLicensePasses(t *testing.T) {
	i := newConnectStateInterceptor()
	st := &httpmw.LicenseState{Active: false, Status: "inactive", Tier: "free"}
	if err := i.enforceStateWithTrialFallback(context.Background(), st, "/everstack.agents.v1.AgentsService/CreateAgent"); err != nil {
		t.Fatalf("inactive license must degrade to CE, not wall: %v", err)
	}
}

func TestConnectDisabledLicenseBlocksWritesAllowsReads(t *testing.T) {
	i := newConnectStateInterceptor()
	st := &httpmw.LicenseState{Active: false, Status: "disabled", Tier: "pro"}

	if err := i.enforceStateWithTrialFallback(context.Background(), st, "/everstack.agents.v1.AgentsService/ListAgents"); err != nil {
		t.Fatalf("disabled license must allow read procedures: %v", err)
	}
	if err := i.enforceStateWithTrialFallback(context.Background(), st, "/everstack.agents.v1.AgentsService/CreateAgent"); err == nil {
		t.Fatal("disabled license must block write procedures")
	}
	// Re-activation must stay reachable from the disabled state.
	if err := i.enforceStateWithTrialFallback(context.Background(), st, "/everstack.gateway.v1.GatewayService/ActivateGatewayInstance"); err != nil {
		t.Fatalf("activation must be allowed under a disabled license: %v", err)
	}
}

func TestConnectActiveAndSuspendedPass(t *testing.T) {
	i := newConnectStateInterceptor()
	for _, st := range []*httpmw.LicenseState{
		{Active: true, Status: "active", Tier: "pro"},
		{Active: true, Status: "suspended", Tier: "pro"},
	} {
		if err := i.enforceStateWithTrialFallback(context.Background(), st, "/everstack.agents.v1.AgentsService/CreateAgent"); err != nil {
			t.Fatalf("status %s must pass: %v", st.Status, err)
		}
	}
}

func TestIsWriteProcedure(t *testing.T) {
	cases := map[string]bool{
		"/svc/CreateAgent":            true,
		"/svc/UpdateAgent":            true,
		"/svc/DeleteAgent":            true,
		"/svc/RunWorkflow":            true,
		"/svc/ListAgents":             false,
		"/svc/GetAgent":               false,
		"/svc/ActivateGatewayInstance": false, // activation whitelist
		"/svc/GenerateActivationToken": false, // activation whitelist
	}
	for proc, want := range cases {
		if got := isWriteProcedure(proc); got != want {
			t.Errorf("isWriteProcedure(%q) = %v, want %v", proc, got, want)
		}
	}
}
