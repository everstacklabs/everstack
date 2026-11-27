package fcagent

import (
	"context"
	"testing"
)

func TestBearerToken_SendsAuthorizationMetadata(t *testing.T) {
	creds := bearerToken("abc123")
	md, err := creds.GetRequestMetadata(context.Background(), "/everstack.firecracker.v1.FirecrackerAgent/NodeInfo")
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	if got := md["authorization"]; got != "Bearer abc123" {
		t.Fatalf("authorization = %q, want %q", got, "Bearer abc123")
	}
	// The gateway dials agents over plain gRPC inside the cluster today;
	// the token must still be attached on an insecure transport.
	if creds.RequireTransportSecurity() {
		t.Fatal("bearer token must not require transport security")
	}
}

func TestResolveAuthToken_ConfigWinsOverEnv(t *testing.T) {
	t.Setenv(authTokenEnv, "from-env")
	if got := resolveAuthToken("from-config"); got != "from-config" {
		t.Fatalf("got %q, want config value", got)
	}
}

func TestResolveAuthToken_FallsBackToEnvAndTrims(t *testing.T) {
	t.Setenv(authTokenEnv, "  from-env \n")
	if got := resolveAuthToken(""); got != "from-env" {
		t.Fatalf("got %q, want trimmed env value", got)
	}
}

func TestResolveAuthToken_EmptyWhenUnset(t *testing.T) {
	t.Setenv(authTokenEnv, "")
	if got := resolveAuthToken(" "); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestBuildDialOptions_AttachesBearerWhenTokenSet(t *testing.T) {
	with, err := buildDialOptions(TLSConfig{}, "tok")
	if err != nil {
		t.Fatalf("buildDialOptions: %v", err)
	}
	without, err := buildDialOptions(TLSConfig{}, "")
	if err != nil {
		t.Fatalf("buildDialOptions: %v", err)
	}
	if len(with) != len(without)+1 {
		t.Fatalf("expected exactly one extra dial option for the bearer token, got %d vs %d", len(with), len(without))
	}
}
