package v1

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/everstacklabs/everstack/internal/enterprise"
)

func TestAgentSaveErrorMapsNameConflict(t *testing.T) {
	t.Parallel()

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: agentNameConstraint}
	tests := []struct {
		name string
		err  error
		want connect.Code
	}{
		{name: "pg error", err: fmt.Errorf("project agent: %w", pgErr), want: connect.CodeAlreadyExists},
		{name: "flattened through the bus", err: errors.New(`duplicate key value violates unique constraint "uq_agent_definitions_tenant_name"`), want: connect.CodeAlreadyExists},
		{name: "other unique constraint", err: &pgconn.PgError{Code: "23505", ConstraintName: "agent_definitions_pkey"}, want: connect.CodeInternal},
		{name: "other error", err: errors.New("connection refused"), want: connect.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := connect.CodeOf(agentSaveError(tt.err, "research-bot"))
			if got != tt.want {
				t.Fatalf("code = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAgentLimitErrorHidesNonQuotaFailures reproduces the toast leak: during
// a DB outage ReserveResourceSlot returned the raw pgx connection error and
// CreateAgent labelled it ResourceExhausted, a code the form shows verbatim.
func TestAgentLimitErrorHidesNonQuotaFailures(t *testing.T) {
	t.Parallel()

	dbErr := fmt.Errorf("failed to check agent limit: %w",
		errors.New(`failed to connect to user=everstack database=everstack_prod: 10.0.3.7:5432 (pg-primary.internal): dial error`))
	got := agentLimitError(dbErr)
	if connect.CodeOf(got) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(got))
	}
	var ce *connect.Error
	if !errors.As(got, &ce) {
		t.Fatalf("not a connect error: %v", got)
	}
	for _, leak := range []string{"everstack_prod", "10.0.3.7", "pg-primary", "user="} {
		if strings.Contains(ce.Message(), leak) {
			t.Fatalf("message %q leaks %q", ce.Message(), leak)
		}
	}

	// The enterprise package tags genuine quota refusals with ErrResourceLimit
	// (covered in internal/enterprise); only those carry user-safe text.
	quota := fmt.Errorf("agent limit reached: 5/5 — upgrade your plan for higher limits: %w", enterprise.ErrResourceLimit)
	got = agentLimitError(quota)
	if connect.CodeOf(got) != connect.CodeResourceExhausted {
		t.Fatalf("quota code = %v, want ResourceExhausted", connect.CodeOf(got))
	}
	if !errors.As(got, &ce) || ce.Message() != quota.Error() {
		t.Fatalf("quota message = %q, want %q", ce.Message(), quota.Error())
	}
}

func TestAgentSaveErrorHidesInternalDetail(t *testing.T) {
	t.Parallel()

	got := agentSaveError(errors.New(`dial tcp 10.0.3.7:5432: connect: connection refused`), "bot")
	var ce *connect.Error
	if !errors.As(got, &ce) || ce.Code() != connect.CodeInternal {
		t.Fatalf("got %v, want Internal", got)
	}
	if strings.Contains(ce.Message(), "10.0.3.7") {
		t.Fatalf("message %q leaks the DB address", ce.Message())
	}
}
