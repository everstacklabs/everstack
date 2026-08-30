package controlplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

var (
	ErrLifecycleNotConfigured     = errors.New("sandbox feature is not configured")
	ErrLifecycleSessionIDRequired = errors.New("session_id is required")
)

type LifecycleRepository interface {
	SetDesiredState(ctx context.Context, id, desired string) error
	GetByID(ctx context.Context, id string) (sandboxlc.Row, error)
}

type LifecycleManager interface {
	StopSandbox(ctx context.Context, sandboxID string) error
	ReviveSandbox(ctx context.Context, sandboxID string) (*sandbox.Instance, error)
	TerminateSandbox(ctx context.Context, sandboxID string) error
	LookupInstanceBySession(ctx context.Context, sessionID string) (*sandbox.Instance, error)
	PurgeTerminatedSandbox(ctx context.Context, sandboxID string) error
	Destroy(ctx context.Context, sessionID string) error
	GetInstanceConfig(ctx context.Context, sandboxID string) (*sandbox.InstanceConfig, string, error)
	GetOrCreate(ctx context.Context, sessionID, tenantID string, config sandbox.SandboxConfig) (*sandbox.Instance, error)
}

type LifecycleService struct {
	repo    LifecycleRepository
	manager LifecycleManager
}

type LifecycleInstance struct {
	ID             string
	Backend        string
	ContainerID    string
	Image          string
	Status         string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Name           string
	LifecycleState string
	AgentHealthy   bool
}

type LifecycleResult struct {
	Success  bool
	Message  string
	Instance *LifecycleInstance
}

type RecreateRequest struct {
	SandboxID string
	SessionID string
	TenantID  string
}

func NewLifecycleService(repo LifecycleRepository, manager LifecycleManager) *LifecycleService {
	return &LifecycleService{repo: repo, manager: manager}
}

func (s *LifecycleService) Stop(ctx context.Context, sandboxID string) (*LifecycleResult, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("sandbox_id is required")
	}
	if s.repo != nil {
		if err := s.repo.SetDesiredState(ctx, sandboxID, sandboxlc.DesireSleeping); err != nil {
			return nil, err
		}
		return &LifecycleResult{Success: true, Message: "Sandbox stop requested (reconciler will converge)"}, nil
	}
	if s.manager == nil {
		return nil, ErrLifecycleNotConfigured
	}
	if err := s.manager.StopSandbox(ctx, sandboxID); err != nil {
		return nil, err
	}
	return &LifecycleResult{Success: true, Message: "Sandbox stopped"}, nil
}

func (s *LifecycleService) Revive(ctx context.Context, sandboxID string) (*LifecycleResult, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("sandbox_id is required")
	}
	if s.repo != nil {
		if err := s.repo.SetDesiredState(ctx, sandboxID, sandboxlc.DesireRunning); err != nil {
			return nil, err
		}
		row, err := s.repo.GetByID(ctx, sandboxID)
		if err != nil {
			return nil, err
		}
		return &LifecycleResult{Instance: lifecycleInstanceFromRow(row)}, nil
	}
	if s.manager == nil {
		return nil, ErrLifecycleNotConfigured
	}
	inst, err := s.manager.ReviveSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return &LifecycleResult{Instance: lifecycleInstanceFromSandbox(inst)}, nil
}

func (s *LifecycleService) Terminate(ctx context.Context, sandboxID string) (*LifecycleResult, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("sandbox_id is required")
	}
	if s.repo != nil {
		if err := s.repo.SetDesiredState(ctx, sandboxID, sandboxlc.DesireTerminated); err != nil {
			return nil, err
		}
		return &LifecycleResult{Success: true, Message: "Sandbox terminate requested (reconciler will converge)"}, nil
	}
	if s.manager == nil {
		return nil, ErrLifecycleNotConfigured
	}
	if err := s.manager.TerminateSandbox(ctx, sandboxID); err != nil {
		return nil, err
	}
	return &LifecycleResult{Success: true, Message: "Sandbox terminated"}, nil
}

func (s *LifecycleService) Destroy(ctx context.Context, sessionID string) (*LifecycleResult, error) {
	if sessionID == "" {
		return nil, ErrLifecycleSessionIDRequired
	}
	if s.manager == nil {
		return nil, ErrLifecycleNotConfigured
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if row, err := s.manager.LookupInstanceBySession(lookupCtx, sessionID); err == nil && row != nil {
		if err := s.manager.PurgeTerminatedSandbox(ctx, row.ID); err != nil {
			return nil, err
		}
		return &LifecycleResult{Success: true, Message: "sandbox record deleted"}, nil
	}
	if err := s.manager.Destroy(ctx, sessionID); err != nil {
		return nil, err
	}
	return &LifecycleResult{Success: true, Message: "sandbox destroyed"}, nil
}

func (s *LifecycleService) Recreate(ctx context.Context, req RecreateRequest) (*sandbox.Instance, error) {
	if req.SandboxID == "" {
		return nil, fmt.Errorf("sandbox_id is required")
	}
	if req.SessionID == "" {
		return nil, ErrLifecycleSessionIDRequired
	}
	if s.manager == nil {
		return nil, ErrLifecycleNotConfigured
	}
	instCfg, _, err := s.manager.GetInstanceConfig(ctx, req.SandboxID)
	if err != nil {
		return nil, err
	}
	sandboxCfg := sandbox.SandboxConfig{
		Enabled:        true,
		Image:          instCfg.Image,
		CPULimit:       instCfg.CPULimit,
		MemoryMB:       instCfg.MemoryMB,
		DiskMB:         instCfg.DiskMB,
		TimeoutSeconds: instCfg.TimeoutSeconds,
		NetworkMode:    string(instCfg.NetworkMode),
		AllowedHosts:   instCfg.AllowedHosts,
		EnvVars:        instCfg.EnvVars,
	}
	recreateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	return s.manager.GetOrCreate(recreateCtx, req.SessionID, req.TenantID, sandboxCfg)
}

func lifecycleInstanceFromRow(row sandboxlc.Row) *LifecycleInstance {
	return &LifecycleInstance{
		ID:             row.ID,
		Backend:        row.Backend,
		ContainerID:    row.ContainerID.String,
		Image:          row.Image,
		Status:         "pending",
		CreatedAt:      row.CreatedAt,
		Name:           row.Name,
		LifecycleState: row.LifecycleState,
	}
}

func lifecycleInstanceFromSandbox(inst *sandbox.Instance) *LifecycleInstance {
	if inst == nil {
		return nil
	}
	return &LifecycleInstance{
		ID:             inst.ID,
		Backend:        inst.Backend,
		ContainerID:    inst.ContainerID,
		Image:          inst.Config.Image,
		Status:         "running",
		CreatedAt:      inst.CreatedAt,
		ExpiresAt:      inst.ExpiresAt,
		Name:           inst.Name,
		LifecycleState: inst.LifecycleState,
		AgentHealthy:   inst.AgentHealthy,
	}
}
