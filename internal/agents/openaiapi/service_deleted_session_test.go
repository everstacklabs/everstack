package openaiapi

import (
	"context"
	"errors"
	"testing"
)

type countingDeletedUpstream struct {
	ambiguousUpstream
	gets int
	err  error
}

func (u *countingDeletedUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	u.gets++
	if u.err != nil {
		return nil, u.err
	}
	return &UpstreamSession{ID: "upstream", Status: "idle", Readiness: "connected"}, nil
}

// newDeletedSessionService keeps environment cleanup active so the existing
// environment guard cannot be what stops the refresh. Only the session's own
// cleanup status varies between these cases.
func newDeletedSessionService(cleanupStatus string) (*Service, *readinessStore, *countingDeletedUpstream, Ownership) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &readinessStore{restartStore: &restartStore{
		owner:      o,
		connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`},
		session: &Session{
			ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream",
			SandboxID: "sandbox", Environment: EnvironmentSpec{Type: "self_hosted"},
			Status: "idle", Readiness: "connected",
			CleanupStatus: cleanupStatus, EnvironmentCleanupStatus: "active",
		},
	}}
	upstream := &countingDeletedUpstream{}
	return &Service{store: store, upstream: upstream, cipher: testCipher{}}, store, upstream, o
}

func TestGetDeletedSessionDoesNotRefreshUpstreamOrWrite(t *testing.T) {
	service, store, upstream, owner := newDeletedSessionService("deleted")
	upstream.err = errors.New("No managed agent resource found")

	for read := 1; read <= 3; read++ {
		got, err := service.GetSession(context.Background(), owner, "session")
		if err != nil {
			t.Fatalf("read %d: %v", read, err)
		}
		if got.CleanupStatus != "deleted" {
			t.Fatalf("read %d: cleanup status = %q, want the durable tombstone", read, got.CleanupStatus)
		}
		if got.LastError != "" {
			t.Fatalf("read %d: deleted session recorded last error %q", read, got.LastError)
		}
	}
	if upstream.gets != 0 {
		t.Fatalf("deleted session issued %d provider calls across three reads, want 0", upstream.gets)
	}
	if store.updates != 0 {
		t.Fatalf("deleted session issued %d writes across three reads, want 0", store.updates)
	}
}

func TestGetActiveSessionStillRefreshesUpstream(t *testing.T) {
	service, store, upstream, owner := newDeletedSessionService("active")
	upstream.err = errors.New("No managed agent resource found")

	if _, err := service.GetSession(context.Background(), owner, "session"); err != nil {
		t.Fatal(err)
	}
	if upstream.gets != 1 {
		t.Fatalf("active session provider calls = %d, want 1", upstream.gets)
	}
	if store.updates != 1 {
		t.Fatalf("active session writes = %d, want 1", store.updates)
	}
}
