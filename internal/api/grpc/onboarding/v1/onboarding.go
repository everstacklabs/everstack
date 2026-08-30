package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	onboardingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/onboarding/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// validPaths is the closed set of onboarding paths the UI can choose. The
// empty string means "no path chosen yet". Anything else is rejected so a
// malformed client can't write garbage into the column.
var validPaths = map[string]struct{}{
	"":           {},
	"agent":      {},
	"gateway":    {},
	"production": {},
}

// onboardingRow is the scan target for the onboarding_state table.
type onboardingRow struct {
	Dismissed        bool      `db:"dismissed"`
	CelebrationShown bool      `db:"celebration_shown"`
	SelectedPath     string    `db:"selected_path"`
	SandboxSkipped   bool      `db:"sandbox_skipped"`
	UpdatedAt        time.Time `db:"updated_at"`
}

// resolveTenantID returns the tenant the request acts on. Context is the trust
// anchor (set by auth middleware); EVS_ORG_ID is the self-hosted single-tenant
// override used only when context is empty. Request fields are never consulted
// — that body-trust pattern produced the 2026-05-06 cross-tenant leak. An empty
// result fails closed.
func resolveTenantID(ctx context.Context) (string, error) {
	tid := contextkeys.GetTenantID(ctx)
	if tid == "" {
		tid = os.Getenv("EVS_ORG_ID")
	}
	if tid == "" {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	return tid, nil
}

func (s *Server) GetOnboardingState(ctx context.Context, _ *connect.Request[onboardingpb.GetOnboardingStateRequest]) (*connect.Response[onboardingpb.GetOnboardingStateResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("onboarding store not configured"))
	}
	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return nil, err
	}

	var row onboardingRow
	err = s.db.GetContext(ctx, &row,
		`SELECT dismissed, celebration_shown, selected_path, sandbox_skipped, updated_at
		   FROM onboarding_state
		  WHERE tenant_id = $1`, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing persisted yet — return a zero-value state so the client
		// falls through to its fresh-start defaults.
		return connect.NewResponse(&onboardingpb.GetOnboardingStateResponse{
			State: &onboardingpb.OnboardingState{},
		}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&onboardingpb.GetOnboardingStateResponse{
		State: rowToState(row),
	}), nil
}

func (s *Server) UpdateOnboardingState(ctx context.Context, req *connect.Request[onboardingpb.UpdateOnboardingStateRequest]) (*connect.Response[onboardingpb.UpdateOnboardingStateResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("onboarding store not configured"))
	}
	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return nil, err
	}

	path := req.Msg.GetSelectedPath()
	if _, ok := validPaths[path]; !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid selected_path %q", path))
	}

	// Full-state replace: the client always sends a complete snapshot, so we
	// upsert every column. updated_at is server-stamped.
	var row onboardingRow
	err = s.db.GetContext(ctx, &row,
		`INSERT INTO onboarding_state
		     (tenant_id, dismissed, celebration_shown, selected_path, sandbox_skipped, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (tenant_id) DO UPDATE SET
		     dismissed         = EXCLUDED.dismissed,
		     celebration_shown = EXCLUDED.celebration_shown,
		     selected_path     = EXCLUDED.selected_path,
		     sandbox_skipped   = EXCLUDED.sandbox_skipped,
		     updated_at        = NOW()
		 RETURNING dismissed, celebration_shown, selected_path, sandbox_skipped, updated_at`,
		tenantID,
		req.Msg.GetDismissed(),
		req.Msg.GetCelebrationShown(),
		path,
		req.Msg.GetSandboxSkipped(),
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&onboardingpb.UpdateOnboardingStateResponse{
		State: rowToState(row),
	}), nil
}

func rowToState(row onboardingRow) *onboardingpb.OnboardingState {
	return &onboardingpb.OnboardingState{
		Dismissed:        row.Dismissed,
		CelebrationShown: row.CelebrationShown,
		SelectedPath:     row.SelectedPath,
		SandboxSkipped:   row.SandboxSkipped,
		UpdatedAt:        timestamppb.New(row.UpdatedAt),
	}
}
