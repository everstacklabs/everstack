package v1

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	api_keypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
)

type fakeCommandBus struct {
	err error
}

func (b *fakeCommandBus) Dispatch(context.Context, commands.Command) error { return b.err }
func (b *fakeCommandBus) RegisterHandler(commands.CommandHandler)          {}

type fakeRegistrar struct {
	calls   []string // tenant|key
	unregs  []string // tenant|key
	err     error    // returned by every call when errs is exhausted
	errs    []error  // returned in order, one per call, before err
	ctxOK   bool
	unregOK bool
}

func (r *fakeRegistrar) next() error {
	if len(r.errs) > 0 {
		e := r.errs[0]
		r.errs = r.errs[1:]
		return e
	}
	return r.err
}

func (r *fakeRegistrar) RegisterAPIKey(ctx context.Context, tenantID, apiKeyID string) error {
	_, r.ctxOK = ctx.Deadline()
	r.calls = append(r.calls, tenantID+"|"+apiKeyID)
	return r.next()
}

func (r *fakeRegistrar) UnregisterAPIKey(ctx context.Context, tenantID, apiKeyID string) error {
	_, r.unregOK = ctx.Deadline()
	r.unregs = append(r.unregs, tenantID+"|"+apiKeyID)
	return r.next()
}

// fastRetry shortens the retry backoff for the test.
func fastRetry(t *testing.T) {
	t.Helper()
	prev := keyRegistrationRetryBackoff
	keyRegistrationRetryBackoff = time.Millisecond
	t.Cleanup(func() { keyRegistrationRetryBackoff = prev })
}

func createKey(t *testing.T, s *Server, bus *fakeCommandBus, tenant string) (*api_keypb.CreateApiKeyResponse, error) {
	t.Helper()
	t.Setenv("EVS_ORG_ID", "")
	ctx := cqrs.WithSystem(context.Background(), &cqrs.System{CommandBus: bus})
	if tenant != "" {
		ctx = contextkeys.WithTenantID(ctx, tenant)
	}
	resp, err := s.CreateApiKey(ctx, connect.NewRequest(&api_keypb.CreateApiKeyRequest{Name: "backend"}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// A created key is registered with billing under the tenant from the
// authenticated context and the id the key was stored with, before the
// response returns, so a spend limit set right after creation finds it.
func TestCreateApiKeyRegistersWithBilling(t *testing.T) {
	reg := &fakeRegistrar{}
	s := &Server{}
	s.SetKeyRegistrar(reg)
	resp, err := createKey(t, s, &fakeCommandBus{}, "org-a")
	if err != nil {
		t.Fatal(err)
	}
	want := "org-a|" + resp.GetApiKey().GetId()
	if len(reg.calls) != 1 || reg.calls[0] != want {
		t.Fatalf("registrar calls = %v, want [%s]", reg.calls, want)
	}
	if !reg.ctxOK {
		t.Fatal("registration ran without a deadline")
	}
}

// A failed registration does not fail key creation (billing then refuses a
// limit on the key until it has traffic); a failed key write registers
// nothing; a server with no registrar (self-hosted) registers nothing.
func TestCreateApiKeyRegistrationFailureModes(t *testing.T) {
	fastRetry(t)
	reg := &fakeRegistrar{err: connect.NewError(connect.CodeUnavailable, errors.New("billing unavailable"))}
	s := &Server{}
	s.SetKeyRegistrar(reg)
	resp, err := createKey(t, s, &fakeCommandBus{}, "org-a")
	if err != nil || resp.GetApiKey().GetId() == "" {
		t.Fatalf("create with billing down: resp %v err %v", resp, err)
	}
	// Billing down: one attempt and exactly one retry, then give up.
	if len(reg.calls) != 2 {
		t.Fatalf("registrar calls = %d, want 2", len(reg.calls))
	}

	reg = &fakeRegistrar{}
	s.SetKeyRegistrar(reg)
	if _, err := createKey(t, s, &fakeCommandBus{err: errors.New("db down")}, "org-a"); err == nil {
		t.Fatal("create with a failed key write: want error")
	}
	if len(reg.calls) != 0 {
		t.Fatalf("a key that was not stored was registered: %v", reg.calls)
	}

	if _, err := createKey(t, &Server{}, &fakeCommandBus{}, "org-a"); err != nil {
		t.Fatalf("no registrar: %v", err)
	}
}

// A transient billing failure is retried once and a recovered retry
// registers the key; a permanent refusal is never retried.
func TestCreateApiKeyRegistrationRetry(t *testing.T) {
	fastRetry(t)
	transient := []connect.Code{
		connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeResourceExhausted,
		connect.CodeAborted, connect.CodeInternal, connect.CodeUnknown,
	}
	for _, code := range transient {
		reg := &fakeRegistrar{errs: []error{connect.NewError(code, errors.New("blip"))}}
		s := &Server{}
		s.SetKeyRegistrar(reg)
		resp, err := createKey(t, s, &fakeCommandBus{}, "org-a")
		if err != nil {
			t.Fatalf("%v: create: %v", code, err)
		}
		want := "org-a|" + resp.GetApiKey().GetId()
		if len(reg.calls) != 2 || reg.calls[0] != want || reg.calls[1] != want {
			t.Fatalf("%v: registrar calls = %v, want the same registration twice", code, reg.calls)
		}
	}

	permanent := []connect.Code{
		connect.CodePermissionDenied, connect.CodeInvalidArgument, connect.CodeAlreadyExists,
		connect.CodeFailedPrecondition, connect.CodeUnauthenticated, connect.CodeNotFound,
		connect.CodeUnimplemented, connect.CodeCanceled,
	}
	for _, code := range permanent {
		reg := &fakeRegistrar{err: connect.NewError(code, errors.New("refused"))}
		s := &Server{}
		s.SetKeyRegistrar(reg)
		if _, err := createKey(t, s, &fakeCommandBus{}, "org-a"); err != nil {
			t.Fatalf("%v: a refused registration failed key creation: %v", code, err)
		}
		if len(reg.calls) != 1 {
			t.Fatalf("%v: registrar calls = %d, want 1 (no retry)", code, len(reg.calls))
		}
	}
}

func deleteKey(t *testing.T, s *Server, bus *fakeCommandBus, tenant, keyID string) (*api_keypb.DeleteApiKeyResponse, error) {
	t.Helper()
	queryBus := query.NewQueryBus()
	queryBus.RegisterHandler(&keyByIDStub{})
	ctx := cqrs.WithSystem(context.Background(), &cqrs.System{CommandBus: bus, QueryBus: queryBus})
	if tenant != "" {
		ctx = contextkeys.WithTenantID(ctx, tenant)
	}
	resp, err := s.DeleteApiKey(ctx, connect.NewRequest(&api_keypb.DeleteApiKeyRequest{Id: keyID}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Deleting a key unregisters it with billing under the tenant from the
// authenticated context (the scope the revocation ran in), never a tenant
// from the request, and only after the revocation was accepted.
func TestDeleteApiKeyUnregistersWithBilling(t *testing.T) {
	fastRetry(t)
	const keyID = "8d7c6b5a-4321-4abc-9def-0123456789ab"
	reg := &fakeRegistrar{}
	s := &Server{}
	s.SetKeyRegistrar(reg)
	if _, err := deleteKey(t, s, &fakeCommandBus{}, "org-a", keyID); err != nil {
		t.Fatal(err)
	}
	if len(reg.unregs) != 1 || reg.unregs[0] != "org-a|"+keyID {
		t.Fatalf("unregister calls = %v, want [org-a|%s]", reg.unregs, keyID)
	}
	if !reg.unregOK {
		t.Fatal("unregister ran without a deadline")
	}
	if len(reg.calls) != 0 {
		t.Fatalf("delete registered a key: %v", reg.calls)
	}

	// No tenant in context: refused before anything reaches billing, and
	// EVS_ORG_ID is not a fallback on delete.
	t.Setenv("EVS_ORG_ID", "org-env")
	reg = &fakeRegistrar{}
	s.SetKeyRegistrar(reg)
	if _, err := deleteKey(t, s, &fakeCommandBus{}, "", keyID); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("no tenant: err = %v, want PermissionDenied", err)
	}
	// A failed revocation unregisters nothing.
	if _, err := deleteKey(t, s, &fakeCommandBus{err: errors.New("db down")}, "org-a", keyID); err == nil {
		t.Fatal("failed revocation: want error")
	}
	if len(reg.unregs) != 0 {
		t.Fatalf("unregistered without a revocation: %v", reg.unregs)
	}
}

// Unregistering is best effort: billing failing (after one retry when
// transient) or refusing never fails the delete. No registrar (self-hosted)
// calls nothing.
func TestDeleteApiKeyUnregisterFailureModes(t *testing.T) {
	fastRetry(t)
	const keyID = "8d7c6b5a-4321-4abc-9def-0123456789ab"

	reg := &fakeRegistrar{err: connect.NewError(connect.CodeUnavailable, errors.New("billing down"))}
	s := &Server{}
	s.SetKeyRegistrar(reg)
	resp, err := deleteKey(t, s, &fakeCommandBus{}, "org-a", keyID)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("delete with billing down: resp %v err %v", resp, err)
	}
	if len(reg.unregs) != 2 {
		t.Fatalf("unregister calls = %d, want 2 (one retry)", len(reg.unregs))
	}

	reg = &fakeRegistrar{errs: []error{connect.NewError(connect.CodeUnavailable, errors.New("blip"))}}
	s.SetKeyRegistrar(reg)
	if _, err := deleteKey(t, s, &fakeCommandBus{}, "org-a", keyID); err != nil {
		t.Fatal(err)
	}
	if len(reg.unregs) != 2 {
		t.Fatalf("recovered retry: unregister calls = %d, want 2", len(reg.unregs))
	}

	reg = &fakeRegistrar{err: connect.NewError(connect.CodeFailedPrecondition, errors.New("unknown tenant"))}
	s.SetKeyRegistrar(reg)
	if _, err := deleteKey(t, s, &fakeCommandBus{}, "org-a", keyID); err != nil {
		t.Fatalf("delete with billing refusing: %v", err)
	}
	if len(reg.unregs) != 1 {
		t.Fatalf("permanent refusal: unregister calls = %d, want 1", len(reg.unregs))
	}

	if _, err := deleteKey(t, &Server{}, &fakeCommandBus{}, "org-a", keyID); err != nil {
		t.Fatalf("no registrar: %v", err)
	}
}
