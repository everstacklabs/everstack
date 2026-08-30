package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/gorilla/mux"
	"google.golang.org/protobuf/types/known/timestamppb"

	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// StopSandbox stops a running sandbox, preserving its trooper snapshot for revival.
//
// When the lifecycle reconciler is wired (LifecycleRepoEnabled), this is a
// 5ms write of desired_state='sleeping' on the row; the reconciler picks
// it up on the next 200ms tick and drives the actual transition. Otherwise
// falls through to the legacy sync sandboxMgr.StopSandbox path.
func (s *Server) StopSandbox(ctx context.Context, req *connect.Request[agentspb.StopSandboxRequest]) (*connect.Response[agentspb.StopSandboxResponse], error) {
	sandboxID := req.Msg.SandboxId
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}

	result, err := sandboxcp.NewLifecycleService(s.lifecycleRepo, s.sandboxMgr).Stop(ctx, sandboxID)
	if err != nil {
		return nil, mapLifecycleError(err)
	}
	return connect.NewResponse(&agentspb.StopSandboxResponse{
		Success: result.Success,
		Message: result.Message,
	}), nil
}

// ReviveSandbox restores a stopped sandbox from its trooper snapshot.
//
// Reconciler path: write desired_state='running'; the reconciler converges
// from sleeping → reviving → running. Returns the current row state so the
// FE has a stable identity to render; status will progress via SSE / poll.
func (s *Server) ReviveSandbox(ctx context.Context, req *connect.Request[agentspb.ReviveSandboxRequest]) (*connect.Response[agentspb.ReviveSandboxResponse], error) {
	sandboxID := req.Msg.SandboxId
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}
	if s.sandboxMgr != nil {
		cfg, _, err := s.sandboxMgr.GetInstanceConfig(ctx, sandboxID)
		if err != nil {
			return nil, mapLifecycleError(err)
		}
		if err := s.sandboxMgr.RequireSandboxBilling(cfg.TenantID); err != nil {
			return nil, mapLifecycleError(err)
		}
		if err := s.sandboxMgr.RequireConcurrentSandboxSlot(ctx, cfg.TenantID, sandboxID); err != nil {
			return nil, mapLifecycleError(err)
		}
		if s.lifecycleRepo != nil {
			if err := s.lifecycleRepo.SetDesiredStateWithLimit(
				ctx,
				sandboxID,
				sandboxlc.DesireRunning,
				s.sandboxMgr.ConcurrentSandboxLimit(cfg.TenantID),
			); err != nil {
				return nil, mapLifecycleError(err)
			}
		}
	}

	result, err := sandboxcp.NewLifecycleService(s.lifecycleRepo, s.sandboxMgr).Revive(ctx, sandboxID)
	if err != nil {
		return nil, mapLifecycleError(err)
	}
	return connect.NewResponse(&agentspb.ReviveSandboxResponse{
		Instance: lifecycleInstanceToProto(result.Instance),
	}), nil
}

// TerminateSandbox permanently destroys a sandbox (non-revivable).
//
// Reconciler path: write desired_state='terminated'; reconciler converges
// through terminating → terminated. The actual VM teardown runs in the
// reconciler tick, not in this RPC, so terminating a wedged VM no longer
// hangs the request.
func (s *Server) TerminateSandbox(ctx context.Context, req *connect.Request[agentspb.TerminateSandboxRequest]) (*connect.Response[agentspb.TerminateSandboxResponse], error) {
	sandboxID := req.Msg.SandboxId
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}

	result, err := sandboxcp.NewLifecycleService(s.lifecycleRepo, s.sandboxMgr).Terminate(ctx, sandboxID)
	if err != nil {
		return nil, mapLifecycleError(err)
	}
	return connect.NewResponse(&agentspb.TerminateSandboxResponse{
		Success: result.Success,
		Message: result.Message,
	}), nil
}

// HandleRestoreFromArchive restores an archived sandbox back to running.
// Uses the same SetDesiredState('running') path as ReviveSandbox --
// the reconciler handles archived→reviving→running transitions.
//
// POST /v1/sandbox/instances/{sandbox_id}/restore
func (s *Server) HandleRestoreFromArchive(w http.ResponseWriter, r *http.Request) {
	if s.lifecycleRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "reconciler not enabled")
		return
	}
	if s.sandboxMgr == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sandbox runtime not enabled")
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	if sandboxID == "" {
		writeJSONError(w, http.StatusBadRequest, "sandbox_id is required")
		return
	}
	if !s.requireSandboxOwnershipHTTP(w, r, sandboxID) {
		return
	}
	if !s.requireSandboxBillingHTTP(w, r) {
		return
	}

	ctx := r.Context()
	cfg, _, err := s.sandboxMgr.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "restore failed: "+err.Error())
		return
	}
	if err := s.lifecycleRepo.SetDesiredStateWithLimit(
		ctx,
		sandboxID,
		sandboxlc.DesireRunning,
		s.sandboxMgr.ConcurrentSandboxLimit(cfg.TenantID),
	); err != nil {
		if errors.Is(err, sandboxlc.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			writeJSONError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "restore failed: "+err.Error())
		return
	}
	row, err := s.lifecycleRepo.GetByID(ctx, sandboxID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":              row.ID,
		"lifecycle_state": sandbox.PublicLifecycleState(row.LifecycleState, sandbox.Status(row.Status)),
		"status":          row.Status,
		"message":         "Restore requested; reconciler will converge archived to restoring to running",
	})
}

// UpdateSandboxAutoIntervals changes the auto-stop/auto-archive/
// auto-delete intervals (minutes, Daytona semantics) on a live
// sandbox. Only supplied fields change. Requires the lifecycle
// reconciler; the legacy path has no minute-interval storage.
func (s *Server) UpdateSandboxAutoIntervals(ctx context.Context, req *connect.Request[agentspb.UpdateSandboxAutoIntervalsRequest]) (*connect.Response[agentspb.UpdateSandboxAutoIntervalsResponse], error) {
	sandboxID := req.Msg.SandboxId
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}
	if !s.LifecycleRepoEnabled() {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("auto intervals require the lifecycle reconciler"))
	}

	stop := req.Msg.AutoStopInterval
	archive := req.Msg.AutoArchiveInterval
	del := req.Msg.AutoDeleteInterval
	if stop != nil && *stop < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auto_stop_interval must be >= 0 (0 disables auto-stop)"))
	}
	if archive != nil && *archive < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auto_archive_interval must be >= 0 (0 disables auto-archive)"))
	}
	if del != nil && *del < -1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auto_delete_interval must be >= -1 (-1 never, 0 ephemeral)"))
	}

	if err := s.lifecycleRepo.UpdateAutoIntervals(ctx, sandboxID, stop, archive, del); err != nil {
		if errors.Is(err, sandboxlc.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	row, err := s.lifecycleRepo.GetByID(ctx, sandboxID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.UpdateSandboxAutoIntervalsResponse{
		Instance: lifecycleRowToProto(&row),
	}), nil
}

// HandleRecoverSandbox re-enters convergence for a sandbox in the
// recoverable 'error' state (Daytona's recover()): convergence resumes
// toward the row's preserved desired_state with the attempt counter
// reset. A sandbox whose VM died (error_reason=vm_not_found) and whose
// desired_state is running is recreated and its workspace restored
// from the stop-time snapshot.
//
// POST /v1/sandbox/instances/{sandbox_id}/recover
func (s *Server) HandleRecoverSandbox(w http.ResponseWriter, r *http.Request) {
	if s.lifecycleRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "reconciler not enabled")
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	if sandboxID == "" {
		writeJSONError(w, http.StatusBadRequest, "sandbox_id is required")
		return
	}
	if !s.requireSandboxOwnershipHTTP(w, r, sandboxID) {
		return
	}

	ctx := r.Context()
	before, err := s.lifecycleRepo.GetByID(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sandboxlc.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if before.DesiredState == sandboxlc.DesireRunning && !s.requireSandboxBillingHTTP(w, r) {
		return
	}
	if err := s.lifecycleRepo.Recover(ctx, sandboxID); err != nil {
		if errors.Is(err, sandboxlc.ErrNotFound) {
			writeJSONError(w, http.StatusConflict, "sandbox is not in a recoverable state")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "recover failed: "+err.Error())
		return
	}
	row, err := s.lifecycleRepo.GetByID(ctx, sandboxID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":              row.ID,
		"lifecycle_state": row.LifecycleState,
		"status":          row.Status,
		"desired_state":   row.DesiredState,
		"message":         "Recover requested; reconciler will re-converge toward the desired state",
	})
}

func mapLifecycleError(err error) error {
	switch {
	case errors.Is(err, sandboxcp.ErrLifecycleNotConfigured):
		return connect.NewError(connect.CodeUnimplemented, ErrSandboxNotConfigured)
	case errors.Is(err, sandboxlc.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, sandbox.ErrLifecycleTransitionRejected):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, sandbox.ErrSandboxBillingRequired):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, sandbox.ErrConcurrentSandboxLimit):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, sandbox.ErrUnsupportedSandboxSize):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, sandbox.ErrUnsupportedBackend):
		return connect.NewError(connect.CodeUnimplemented, err)
	case strings.Contains(strings.ToLower(err.Error()), "not found"):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func lifecycleInstanceToProto(inst *sandboxcp.LifecycleInstance) *agentspb.SandboxInstance {
	if inst == nil {
		return nil
	}
	pb := &agentspb.SandboxInstance{
		Id:             inst.ID,
		Backend:        publicBackendLabel(inst.Backend),
		ContainerId:    inst.ContainerID,
		Image:          inst.Image,
		Status:         sandboxStatusFromString(inst.Status),
		CreatedAt:      timestamppb.New(inst.CreatedAt),
		Name:           inst.Name,
		LifecycleState: sandbox.PublicLifecycleState(inst.LifecycleState, sandbox.Status(inst.Status)),
		AgentHealthy:   inst.AgentHealthy,
	}
	if !inst.ExpiresAt.IsZero() {
		pb.ExpiresAt = timestamppb.New(inst.ExpiresAt)
	}
	return pb
}
