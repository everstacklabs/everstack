package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	usagepkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/observability"
	upstreampkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/upstream"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/google/uuid"
)

type Config struct {
	AllowedModels    []string
	EnvironmentImage string
	ExecutorVersion  string
	LeaseDuration    time.Duration
}

type Service struct {
	store            Store
	cipher           Cipher
	upstream         Upstream
	environments     EnvironmentManager
	tools            ToolController
	approvals        ApprovalController
	usage            *usagepkg.Recorder
	artifacts        ArtifactController
	recoveryAuth     RecoveryAuthorizer
	config           Config
	supervisorMu     sync.Mutex
	supervising      map[string]context.CancelFunc
	supervisorCtx    context.Context
	supervisorCancel context.CancelFunc
	supervisorWG     sync.WaitGroup
	closing          bool
	holderID         string
	leaseDuration    time.Duration
	leaseMu          sync.Mutex
	leasing          map[string]*sessionOwnership
}

func NewService(store Store, cipher Cipher, upstream Upstream, environments EnvironmentManager, tools ToolController, cfg Config) (*Service, error) {
	if store == nil {
		return nil, ErrUnavailable
	}
	if cipher == nil {
		return nil, fmt.Errorf("OpenAI Agents credential encryption is not configured: %w", ErrUnavailable)
	}
	supervisorCtx, cancel := context.WithCancel(context.Background())
	leaseDuration := cfg.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	return &Service{store: store, cipher: cipher, upstream: upstream, environments: environments, tools: tools, config: cfg, supervising: map[string]context.CancelFunc{}, supervisorCtx: supervisorCtx, supervisorCancel: cancel, holderID: uuid.NewString(), leaseDuration: leaseDuration, leasing: map[string]*sessionOwnership{}}, nil
}
func (s *Service) SetTools(tools ToolController)                       { s.tools = tools }
func (s *Service) SetApprovals(approvals ApprovalController)           { s.approvals = approvals }
func (s *Service) SetUsageRecorder(usage *usagepkg.Recorder)           { s.usage = usage }
func (s *Service) SetArtifacts(artifacts ArtifactController)           { s.artifacts = artifacts }
func (s *Service) SetRecoveryAuthorizer(authorizer RecoveryAuthorizer) { s.recoveryAuth = authorizer }

func (s *Service) UploadSessionInput(ctx context.Context, o Ownership, id, name, contentType string, content []byte, destination string) (*ArtifactDTO, error) {
	if err := authorize(o, "session:write"); err != nil {
		return nil, err
	}
	if s.artifacts == nil {
		return nil, ErrUnavailable
	}
	return s.artifacts.Upload(ctx, o, id, name, contentType, content, destination)
}
func (s *Service) ListSessionArtifacts(ctx context.Context, o Ownership, id string) ([]ArtifactDTO, error) {
	if err := authorize(o, "session:read"); err != nil {
		return nil, err
	}
	if s.artifacts == nil {
		return nil, ErrUnavailable
	}
	return s.artifacts.List(ctx, o, id)
}
func (s *Service) ExportSessionArtifact(ctx context.Context, o Ownership, id, path string) (*ArtifactDTO, error) {
	if err := authorize(o, "session:write"); err != nil {
		return nil, err
	}
	if s.artifacts == nil {
		return nil, ErrUnavailable
	}
	return s.artifacts.Export(ctx, o, id, path)
}
func (s *Service) GetArtifactDownload(ctx context.Context, o Ownership, id, objectID string) (*ArtifactDownload, error) {
	if err := authorize(o, "session:read"); err != nil {
		return nil, err
	}
	if s.artifacts == nil {
		return nil, ErrUnavailable
	}
	return s.artifacts.Download(ctx, o, id, objectID)
}
func (s *Service) DeleteSessionArtifact(ctx context.Context, o Ownership, id, objectID string) error {
	if err := authorize(o, "session:write"); err != nil {
		return err
	}
	if s.artifacts == nil {
		return ErrUnavailable
	}
	return s.artifacts.Delete(ctx, o, id, objectID)
}

func (s *Service) Close(ctx context.Context) error {
	s.supervisorMu.Lock()
	s.closing = true
	s.supervisorCancel()
	s.supervisorMu.Unlock()
	done := make(chan struct{})
	go func() { s.supervisorWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func authorize(o Ownership, action string) error {
	if strings.TrimSpace(o.OrganizationID) == "" || strings.TrimSpace(o.InstanceID) == "" || strings.TrimSpace(o.PrincipalID) == "" {
		return ErrForbidden
	}
	role := strings.ToLower(strings.TrimSpace(o.Role))
	if role == "" && strings.HasPrefix(o.PrincipalID, "apikey:") {
		role = "service"
	}
	allowed := map[string]map[string]bool{
		"connection:read":  {"owner": true, "admin": true, "member": true, "viewer": true, "service": true},
		"connection:write": {"owner": true, "admin": true},
		"session:read":     {"owner": true, "admin": true, "member": true, "viewer": true, "service": true},
		"session:write":    {"owner": true, "admin": true, "member": true},
		"approval:resolve": {"owner": true, "admin": true},
	}
	if !allowed[action][role] {
		return ErrForbidden
	}
	return nil
}
func (s *Service) ListApprovals(ctx context.Context, o Ownership) ([]Approval, error) {
	if err := authorize(o, "session:read"); err != nil {
		return nil, err
	}
	if s.approvals == nil {
		return nil, ErrUnavailable
	}
	return s.approvals.ListPending(ctx, o)
}
func (s *Service) ResolveApproval(ctx context.Context, o Ownership, id, decision string) error {
	if err := authorize(o, "approval:resolve"); err != nil {
		return err
	}
	if s.approvals == nil {
		return ErrUnavailable
	}
	if decision != "approved" && decision != "denied" {
		return ErrInvalid
	}
	review, err := s.approvals.Review(ctx, o, id)
	if err != nil {
		return err
	}
	if err = s.approvals.Resolve(ctx, o, id, decision); err != nil {
		return err
	}
	x, secret, err := s.sessionSecret(ctx, o, review.SessionID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"turn_id": review.TurnID, "call_id": review.CallID, "type": "function_call", "name": review.ToolName, "arguments": review.Arguments})
	results, err := s.tools.HandleRequiredActions(ctx, o, review.SessionID, UpstreamEvent{ActionPayload: payload})
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.State == "pending" {
			return ErrCreatePending
		}
		if _, err = s.submitToolResult(ctx, o, review.SessionID, secret, x.UpstreamSessionID, result); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) submitToolResult(ctx context.Context, o Ownership, localID string, secret ConnectionSecret, upstreamID string, result ToolResult) (bool, error) {
	submitter, ok := s.upstream.(ToolResultSubmitter)
	if !ok {
		return false, ErrUnavailable
	}
	delivery, ok := s.tools.(ToolDeliveryController)
	if !ok {
		return false, ErrUnavailable
	}
	token := uuid.NewString()
	claimLease := 10 * time.Minute
	if timeoutSource, available := s.upstream.(interface{ ToolResultTimeout() time.Duration }); available {
		if timeout := timeoutSource.ToolResultTimeout(); timeout > 0 && 2*timeout > claimLease {
			claimLease = 2 * timeout
		}
	}
	claimed, recovery, err := delivery.ClaimToolResultDelivery(ctx, o, localID, result, token, claimLease)
	if err != nil {
		return false, err
	}
	if !claimed {
		if recovery == "submitted" {
			return true, s.persistToolResult(ctx, o, localID, result)
		}
		return false, nil
	}
	if recovery == "expired" || recovery == "uncertain" {
		checker, available := s.upstream.(ToolResultAcceptanceChecker)
		if !available {
			_ = delivery.MarkToolResultDeliveryUncertain(ctx, o, localID, result, token)
			return false, fmt.Errorf("tool result delivery recovery requires upstream reconciliation")
		}
		accepted, checkErr := checker.ToolResultAccepted(ctx, secret, upstreamID, result)
		if checkErr != nil {
			if markErr := delivery.MarkToolResultDeliveryUncertain(ctx, o, localID, result, token); markErr != nil {
				return false, errors.Join(checkErr, markErr)
			}
			return false, checkErr
		}
		if accepted {
			if completeErr := delivery.CompleteToolResultDelivery(ctx, o, localID, result, token); completeErr != nil {
				return false, completeErr
			}
			return true, s.persistToolResult(ctx, o, localID, result)
		}
		if recovery == "uncertain" || recovery == "expired" {
			if markErr := delivery.MarkToolResultDeliveryUncertain(ctx, o, localID, result, token); markErr != nil {
				return false, markErr
			}
			return false, fmt.Errorf("tool result delivery remains uncertain")
		}
	}
	if err = submitter.SubmitToolResult(ctx, secret, upstreamID, result); err != nil {
		if isAmbiguous(err) {
			if markErr := delivery.MarkToolResultDeliveryUncertain(ctx, o, localID, result, token); markErr != nil {
				return false, errors.Join(err, markErr)
			}
			return false, err
		}
		if releaseErr := delivery.ReleaseToolResultDelivery(ctx, o, localID, result, token); releaseErr != nil {
			return false, errors.Join(err, releaseErr)
		}
		return false, err
	}
	if err = delivery.CompleteToolResultDelivery(ctx, o, localID, result, token); err != nil {
		return false, err
	}
	return true, s.persistToolResult(ctx, o, localID, result)
}

func (s *Service) persistToolResult(ctx context.Context, o Ownership, localID string, result ToolResult) error {
	_, _, err := s.store.PutEvent(ctx, o, localID, UpstreamEvent{ID: "tool-result:" + result.TurnID + ":" + result.CallID, Type: "everstack.tool.result", Version: "everstack=v1", ItemID: result.CallID, Payload: sanitizeJSON(json.RawMessage(fmt.Sprintf(`{"state":%q,"success":%t,"error":%q}`, result.State, result.Success, result.Error))), Failed: !result.Success})
	return err
}

func (s *Service) Capabilities(ctx context.Context, o Ownership) (Capabilities, error) {
	if err := authorize(o, "session:read"); err != nil {
		return Capabilities{}, err
	}
	modes := []string{"none"}
	if s.environments != nil && strings.TrimSpace(s.config.EnvironmentImage) != "" && s.environments.ValidateConfiguration(ctx) == nil {
		modes = append(modes, "self_hosted")
	}
	tools := []string{}
	if s.tools != nil {
		tools = []string{"function", "mcp"}
	}
	files := []string{}
	if s.artifacts != nil {
		files = []string{"upload_input", "attach_input", "export", "list", "download", "delete"}
	}
	readiness := "pending"
	if connections, err := s.store.ListConnections(ctx, o); err == nil {
		for _, c := range connections {
			if c.Status == "connected" {
				readiness = "connected"
				break
			}
		}
	}
	return Capabilities{Enabled: true, Provider: Provider, EnvironmentModes: modes, ToolTypes: tools, FileOperations: files, ExecutorVersion: s.config.ExecutorVersion, Readiness: readiness, CanManageConnections: authorize(o, "connection:write") == nil, CanRunSessions: authorize(o, "session:write") == nil, CanResolveApprovals: authorize(o, "approval:resolve") == nil}, nil
}

func (s *Service) CreateConnection(ctx context.Context, o Ownership, r CreateConnectionRequest) (*Connection, error) {
	if err := authorize(o, "connection:write"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.APIKey) == "" {
		return nil, ErrInvalid
	}
	if strings.TrimSpace(r.ExecutorKey) != "" && strings.TrimSpace(r.ExecutorKey) == strings.TrimSpace(r.APIKey) {
		return nil, fmt.Errorf("executor credential must differ from application credential: %w", ErrInvalid)
	}
	allowed := append([]string(nil), s.config.AllowedModels...)
	if len(r.AllowedModels) > 0 {
		if len(allowed) == 0 {
			return nil, fmt.Errorf("caller model allowlists are disabled: %w", ErrInvalid)
		}
		for _, m := range r.AllowedModels {
			if !slices.Contains(allowed, m) {
				return nil, fmt.Errorf("model %q is not permitted: %w", m, ErrInvalid)
			}
		}
		allowed = append([]string(nil), r.AllowedModels...)
	}
	secret, err := s.encryptSecret(ConnectionSecret{APIKey: r.APIKey})
	if err != nil {
		return nil, err
	}
	executor := ""
	if r.ExecutorKey != "" {
		executor, err = s.encryptSecret(ConnectionSecret{ExecutorKey: r.ExecutorKey})
		if err != nil {
			return nil, err
		}
	}
	c := Connection{ID: uuid.NewString(), Name: strings.TrimSpace(r.Name), Provider: Provider, ProjectID: strings.TrimSpace(r.ProjectID), OpenAIOrganization: strings.TrimSpace(r.OrganizationID), OpenAIPrincipal: strings.TrimSpace(r.PrincipalID), AllowedModels: allowed}
	created, err := s.store.CreateConnection(ctx, o, c, secret, executor)
	if err != nil {
		return nil, err
	}
	if s.upstream == nil {
		return created, nil
	}
	checkSecret := ConnectionSecret{APIKey: r.APIKey, ProjectID: c.ProjectID, OrganizationID: c.OpenAIOrganization}
	if err = s.upstream.ValidateConnection(ctx, checkSecret); err != nil {
		if persistErr := s.store.UpdateConnectionStatus(ctx, o, c.ID, "failed", err.Error()); persistErr != nil {
			return nil, persistErr
		}
		return nil, err
	}
	if err = s.store.UpdateConnectionStatus(ctx, o, c.ID, "connected", ""); err != nil {
		return nil, err
	}
	return s.store.GetConnection(ctx, o, c.ID)
}
func (s *Service) ListConnections(ctx context.Context, o Ownership) ([]Connection, error) {
	if err := authorize(o, "connection:read"); err != nil {
		return nil, err
	}
	return s.store.ListConnections(ctx, o)
}
func (s *Service) GetConnection(ctx context.Context, o Ownership, id string) (*Connection, error) {
	if err := authorize(o, "connection:read"); err != nil {
		return nil, err
	}
	return s.store.GetConnection(ctx, o, id)
}
func (s *Service) UpdateConnection(ctx context.Context, o Ownership, id string, r UpdateConnectionRequest) (*Connection, error) {
	if err := authorize(o, "connection:write"); err != nil {
		return nil, err
	}
	c, err := s.store.GetConnection(ctx, o, id)
	if err != nil {
		return nil, err
	}
	app, executorRef := c.credentialRef, c.executorRef
	if r.Name != nil {
		c.Name = strings.TrimSpace(*r.Name)
		if c.Name == "" {
			return nil, ErrInvalid
		}
	}
	if r.ProjectID != nil {
		c.ProjectID = strings.TrimSpace(*r.ProjectID)
	}
	if r.OrganizationID != nil {
		c.OpenAIOrganization = strings.TrimSpace(*r.OrganizationID)
	}
	if r.PrincipalID != nil {
		c.OpenAIPrincipal = strings.TrimSpace(*r.PrincipalID)
	}
	if r.AllowedModels != nil {
		for _, m := range *r.AllowedModels {
			if !slices.Contains(s.config.AllowedModels, m) {
				return nil, ErrForbidden
			}
		}
		c.AllowedModels = append([]string(nil), (*r.AllowedModels)...)
	}
	if r.APIKey != nil {
		if strings.TrimSpace(*r.APIKey) == "" {
			return nil, ErrInvalid
		}
		app, err = s.encryptSecret(ConnectionSecret{APIKey: *r.APIKey})
		if err != nil {
			return nil, err
		}
	}
	if r.ExecutorKey != nil {
		if *r.ExecutorKey == "" {
			executorRef = ""
		} else {
			executorRef, err = s.encryptSecret(ConnectionSecret{ExecutorKey: *r.ExecutorKey})
			if err != nil {
				return nil, err
			}
		}
	}
	appCheck, appCheckErr := s.decryptSecret(app)
	if appCheckErr != nil {
		return nil, appCheckErr
	}
	if executorRef != "" {
		executorCheck, executorCheckErr := s.decryptSecret(executorRef)
		if executorCheckErr != nil {
			return nil, executorCheckErr
		}
		if strings.TrimSpace(appCheck.APIKey) == strings.TrimSpace(executorCheck.ExecutorKey) {
			return nil, fmt.Errorf("executor credential must differ from application credential: %w", ErrInvalid)
		}
	}
	updated, err := s.store.UpdateConnection(ctx, o, *c, app, executorRef)
	if err != nil {
		return nil, err
	}
	if s.upstream == nil {
		return updated, nil
	}
	_, secret, err := s.connectionSecret(ctx, o, id)
	if err != nil {
		return nil, err
	}
	if err = s.upstream.ValidateConnection(ctx, secret); err != nil {
		if persistErr := s.store.UpdateConnectionStatus(ctx, o, id, "failed", err.Error()); persistErr != nil {
			return nil, persistErr
		}
		return nil, err
	}
	if err = s.store.UpdateConnectionStatus(ctx, o, id, "connected", ""); err != nil {
		return nil, err
	}
	return s.store.GetConnection(ctx, o, id)
}
func (s *Service) DeleteConnection(ctx context.Context, o Ownership, id string) error {
	if err := authorize(o, "connection:write"); err != nil {
		return err
	}
	return s.store.DeleteConnection(ctx, o, id)
}

func (s *Service) CreateSession(ctx context.Context, o Ownership, r CreateSessionRequest) (*Session, error) {
	if err := authorize(o, "session:write"); err != nil {
		return nil, err
	}
	if s.upstream == nil {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(r.ConnectionID) == "" || strings.TrimSpace(r.IdempotencyKey) == "" || strings.TrimSpace(r.Model) == "" {
		return nil, ErrInvalid
	}
	if r.Environment.Type == "" {
		r.Environment.Type = "none"
	}
	if r.Environment.Type != "none" && r.Environment.Type != "self_hosted" {
		return nil, fmt.Errorf("environment mode %q is unsupported: %w", r.Environment.Type, ErrInvalid)
	}
	if r.Environment.Type == "self_hosted" && s.environments == nil {
		return nil, fmt.Errorf("self_hosted environment is unavailable: %w", ErrUnavailable)
	}
	if r.Environment.Type == "self_hosted" {
		if err := s.environments.ValidateConfiguration(ctx); err != nil {
			return nil, err
		}
	}
	if r.Environment.Type == "self_hosted" && strings.TrimSpace(s.config.EnvironmentImage) == "" {
		return nil, fmt.Errorf("self_hosted executor image is not configured: %w", ErrUnavailable)
	}
	c, secret, err := s.connectionSecret(ctx, o, r.ConnectionID)
	if err != nil {
		return nil, err
	}
	if len(c.AllowedModels) > 0 && !slices.Contains(c.AllowedModels, r.Model) {
		return nil, fmt.Errorf("model %q is not permitted by connection: %w", r.Model, ErrForbidden)
	}
	if r.Environment.Type == "self_hosted" && c.executorRef == "" {
		return nil, fmt.Errorf("executor credential is not configured: %w", ErrInvalid)
	}
	var resolved ResolvedTools
	if s.tools != nil {
		resolved, err = s.tools.ResolveTools(ctx, o, r.Tools)
		if err != nil {
			return nil, err
		}
		r.resolvedTools = resolved.Configs
	} else if len(r.Tools) > 0 {
		return nil, fmt.Errorf("tools are unavailable: %w", ErrUnavailable)
	}
	x := Session{ID: uuid.NewString(), Provider: Provider, ConnectionID: r.ConnectionID, Model: r.Model, Environment: r.Environment, IdempotencyKey: r.IdempotencyKey, RequestFingerprint: fingerprint(r)}
	intent, created, err := s.store.BeginSessionCreate(ctx, o, x)
	if err != nil {
		return nil, err
	}
	if s.tools != nil && len(r.Tools) > 0 {
		if err = s.tools.BindSession(ctx, o, intent.ID, resolved); err != nil {
			_ = s.store.UpdateSessionState(ctx, o, intent.ID, "failed", "failed", err.Error())
			return nil, err
		}
	}
	if !created {
		if intent.RequestFingerprint != x.RequestFingerprint {
			return nil, fmt.Errorf("idempotency key was used with different session input: %w", ErrInvalid)
		}
		_, savedSecret, secretErr := s.connectionSecret(ctx, o, intent.ConnectionID)
		if secretErr != nil {
			return nil, secretErr
		}
		return s.recoverCreate(ctx, o, nil, savedSecret, intent)
	}
	u, err := s.upstream.CreateSession(ctx, secret, r)
	if err != nil {
		if isAmbiguous(err) {
			if persistErr := s.store.MarkSessionCreateAmbiguous(ctx, o, intent.ID, err.Error()); persistErr != nil {
				return nil, persistErr
			}
			return s.store.GetSession(ctx, o, intent.ID)
		}
		if persistErr := s.store.UpdateSessionState(ctx, o, intent.ID, "failed", "failed", err.Error()); persistErr != nil {
			return nil, persistErr
		}
		return nil, err
	}
	createdSession, err := s.store.CompleteSessionCreate(ctx, o, intent.ID, *u)
	if err != nil {
		return nil, err
	}
	if r.Environment.Type == "self_hosted" {
		owned, _, leaseErr := s.acquireSessionOwnership(ctx, o, intent.ID)
		if leaseErr != nil {
			return nil, leaseErr
		}
		if !owned {
			return nil, ErrCreatePending
		}
		if c.executorRef == "" {
			_ = s.store.UpdateSessionState(ctx, o, intent.ID, "failed", "failed", "executor credential is not configured")
			return nil, ErrUnavailable
		}
		executorSecret, decryptErr := s.decryptSecret(c.executorRef)
		if decryptErr != nil {
			return nil, decryptErr
		}
		if strings.TrimSpace(executorSecret.ExecutorKey) == strings.TrimSpace(secret.APIKey) {
			return nil, fmt.Errorf("executor credential must differ from application credential: %w", ErrInvalid)
		}
		env, provisionErr := s.environments.Provision(ctx, EnvironmentProvisionRequest{
			Ownership: o, LocalSessionID: intent.ID, UpstreamEnvironmentID: u.EnvironmentID,
			RemoteURL: u.RemoteURL, ExecutorAPIKey: executorSecret.ExecutorKey,
			WorkspaceDirectory: r.Environment.WorkspaceDirectory, Image: s.config.EnvironmentImage,
			Lifetime:            s.sessionOwnershipContext(intent.ID),
			FenceStartupTimeout: s.environmentTimeoutFence(o, intent.ID),
		})
		// Provisioning can fail after the sandbox has been durably created. Keep
		// that authenticated reference so a later delete can find and destroy the
		// resource from any replica.
		if env != nil && strings.TrimSpace(env.SandboxID) != "" {
			if bindErr := s.store.BindEnvironment(ctx, o, intent.ID, env.SandboxID, env.Status); bindErr != nil {
				compensationCtx, cancelCompensation := context.WithTimeout(context.Background(), 30*time.Second)
				compensationErr := s.environments.DeleteReference(compensationCtx, o, intent.ID, env.SandboxID)
				combinedErr := errors.Join(provisionErr, bindErr, compensationErr)
				stateErr := s.store.UpdateSessionState(compensationCtx, o, intent.ID, "failed", "failed", sanitizeError(combinedErr.Error()))
				cancelCompensation()
				return nil, errors.Join(combinedErr, stateErr)
			}
		}
		if provisionErr != nil {
			_ = s.store.UpdateSessionState(ctx, o, intent.ID, "failed", "failed", provisionErr.Error())
			return nil, provisionErr
		}
		if env == nil || strings.TrimSpace(env.SandboxID) == "" {
			missingErr := errors.New("environment provision returned no sandbox reference")
			_ = s.store.UpdateSessionState(ctx, o, intent.ID, "failed", "failed", missingErr.Error())
			return nil, missingErr
		}
		createdSession, err = s.store.GetSession(ctx, o, intent.ID)
	}
	if err != nil {
		return nil, err
	}
	if createdSession == nil {
		return nil, ErrUnavailable
	}
	s.startSupervisor(o, createdSession.ID, createdSession.UpstreamSessionID)
	return createdSession, err
}
func (s *Service) recoverCreate(ctx context.Context, o Ownership, c *Connection, secret ConnectionSecret, x *Session) (*Session, error) {
	if x.UpstreamSessionID != "" {
		return x, nil
	}
	if x.Status != "creating" && x.Status != "recovery_pending" {
		return x, nil
	}
	u, err := s.upstream.RecoverSession(ctx, secret, x.IdempotencyKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) || isAmbiguous(err) {
			return x, ErrCreatePending
		}
		return nil, err
	}
	return s.store.CompleteSessionCreate(ctx, o, x.ID, *u)
}
func (s *Service) ListSessions(ctx context.Context, o Ownership) ([]Session, error) {
	if err := authorize(o, "session:read"); err != nil {
		return nil, err
	}
	return s.store.ListSessions(ctx, o)
}
func (s *Service) GetSession(ctx context.Context, o Ownership, id string) (*Session, error) {
	if err := authorize(o, "session:read"); err != nil {
		return nil, err
	}
	x, err := s.store.GetSession(ctx, o, id)
	if err != nil {
		return nil, err
	}
	if x.EnvironmentCleanupStatus != "" && x.EnvironmentCleanupStatus != "active" {
		return x, nil
	}
	// A deleted session has no upstream resource left to read. Refreshing it
	// asks the provider about a record it already removed, which answers
	// "not found" forever, and each answer is written back as last_error and
	// a new updated_at. Reads must not churn provider calls or rows for a
	// session whose lifecycle is over.
	if x.CleanupStatus != "" && x.CleanupStatus != "active" {
		return x, nil
	}
	// A self-hosted create can succeed upstream before local executor
	// provisioning fails. Preserve that durable local failure instead of
	// replacing it with the upstream session's requires_action state.
	if x.Environment.Type == "self_hosted" && x.SandboxID == "" && x.Status == "failed" && x.Readiness == "failed" {
		return x, nil
	}
	if x.UpstreamSessionID == "" || s.upstream == nil {
		return x, nil
	}
	_, secret, secretErr := s.connectionSecret(ctx, o, x.ConnectionID)
	if secretErr != nil {
		return nil, secretErr
	}
	remote, refreshErr := s.upstream.GetSession(ctx, secret, x.UpstreamSessionID)
	if refreshErr != nil {
		if x.Environment.Type == "self_hosted" {
			_ = s.store.UpdateSessionStatus(ctx, o, id, x.Status, refreshErr.Error())
		} else {
			_ = s.store.UpdateSessionState(ctx, o, id, x.Status, x.Readiness, refreshErr.Error())
		}
		return s.store.GetSession(ctx, o, id)
	}
	if x.Environment.Type == "self_hosted" {
		err = s.store.UpdateSessionStatus(ctx, o, id, defaultString(remote.Status, x.Status), "")
	} else {
		err = s.store.UpdateSessionState(ctx, o, id, defaultString(remote.Status, x.Status), defaultString(remote.Readiness, x.Readiness), "")
	}
	if err != nil {
		return nil, err
	}
	owned, newlyOwned, ownershipErr := s.acquireSessionOwnership(ctx, o, id)
	if ownershipErr != nil {
		return nil, ownershipErr
	}
	if owned {
		if newlyOwned && x.Environment.Type == "self_hosted" && x.SandboxID != "" && s.environments != nil {
			c, _, credentialErr := s.connectionSecret(ctx, o, x.ConnectionID)
			if credentialErr != nil || c.executorRef == "" {
				if credentialErr == nil {
					credentialErr = ErrUnavailable
				}
				return nil, credentialErr
			}
			executorSecret, decryptErr := s.decryptSecret(c.executorRef)
			if decryptErr != nil {
				return nil, decryptErr
			}
			if _, reconcileErr := s.environments.Reconcile(ownedContext(ctx, o), EnvironmentReconcileRequest{Ownership: o, LocalSessionID: id, UpstreamEnvironmentID: x.UpstreamEnvironmentID, SandboxID: x.SandboxID, RemoteURL: remote.RemoteURL, ExecutorAPIKey: executorSecret.ExecutorKey, Status: x.Readiness, Lifetime: s.sessionOwnershipContext(id), FenceStartupTimeout: s.environmentTimeoutFence(o, id)}); reconcileErr != nil {
				return nil, reconcileErr
			}
		}
		s.startSupervisor(o, id, x.UpstreamSessionID)
	}
	return s.store.GetSession(ctx, o, id)
}
func (s *Service) SendInput(ctx context.Context, o Ownership, id string, r InputRequest) error {
	if err := authorize(o, "session:write"); err != nil {
		return err
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" || len(r.Input) == 0 {
		return ErrInvalid
	}
	x, secret, err := s.sessionSecret(ctx, o, id)
	if err != nil {
		return err
	}
	if x.EnvironmentCleanupStatus != "" && x.EnvironmentCleanupStatus != "active" {
		return fmt.Errorf("session environment is terminal: %w", ErrUnavailable)
	}
	inputFingerprint := fingerprint(r.Input)
	claimed, err := s.store.ClaimInput(ctx, o, id, r.IdempotencyKey, inputFingerprint)
	if err != nil {
		return err
	}
	if !claimed {
		status, savedFingerprint, statusErr := s.store.InputStatus(ctx, o, id, r.IdempotencyKey)
		if statusErr != nil {
			return statusErr
		}
		if savedFingerprint != inputFingerprint {
			return fmt.Errorf("idempotency key was used with different input: %w", ErrInvalid)
		}
		if status == "sent" {
			return nil
		}
		return fmt.Errorf("input intent is %s: %w", status, ErrCreatePending)
	}
	err = s.upstream.SendInput(ctx, secret, x.UpstreamSessionID, r)
	if err != nil {
		status := "failed"
		if isAmbiguous(err) {
			status = "recovery_pending"
		}
		if persistErr := s.store.CompleteInput(ctx, o, id, r.IdempotencyKey, status); persistErr != nil {
			return persistErr
		}
		return err
	}
	return s.store.CompleteInput(ctx, o, id, r.IdempotencyKey, "sent")
}
func (s *Service) Cancel(ctx context.Context, o Ownership, id string) error {
	if err := authorize(o, "session:write"); err != nil {
		return err
	}
	x, secret, err := s.sessionSecret(ctx, o, id)
	if err != nil {
		return err
	}
	if err = s.upstream.CancelSession(ctx, secret, x.UpstreamSessionID); err != nil {
		return err
	}
	return s.store.UpdateSessionState(ctx, o, id, "canceling", x.Readiness, "")
}
func (s *Service) DeleteSession(ctx context.Context, o Ownership, id string) error {
	if err := authorize(o, "session:write"); err != nil {
		return err
	}
	x, secret, err := s.sessionSecret(ctx, o, id)
	if err != nil {
		return err
	}
	upstreamDeleted := x.CleanupStatus == "upstream_deleted"
	if !upstreamDeleted {
		if err = s.store.MarkCleanup(ctx, o, id, "deleting", ""); err != nil {
			return err
		}
	}
	if err = s.stopSupervisor(ctx, id); err != nil {
		return err
	}
	if x.UpstreamSessionID != "" && !upstreamDeleted {
		// A session with an active turn cannot be deleted upstream. Cancellation is
		// idempotent; wait for the provider to publish a terminal turn state before
		// attempting cleanup.
		forceCancel := x.Environment.Type == "self_hosted" && x.SandboxID == "" && x.Readiness == "failed"
		remote, getErr := s.upstream.GetSession(ctx, secret, x.UpstreamSessionID)
		if isUpstreamNotFound(getErr) {
			if err = s.store.MarkCleanup(ctx, o, id, "upstream_deleted", ""); err != nil {
				return err
			}
			upstreamDeleted = true
		} else if getErr != nil || remote == nil {
			if getErr == nil {
				getErr = errors.New("upstream session lookup returned no session")
			}
			_ = s.store.MarkCleanup(ctx, o, id, "retry_pending", sanitizeError(getErr.Error()))
			return getErr
		}
		if !upstreamDeleted && (forceCancel || !upstreamSessionDeletable(remote)) {
			if cancelErr := s.upstream.CancelSession(ctx, secret, x.UpstreamSessionID); cancelErr != nil {
				_ = s.store.MarkCleanup(ctx, o, id, "retry_pending", cancelErr.Error())
				return cancelErr
			}
			pollCtx, cancelPoll := context.WithTimeout(ctx, 5*time.Second)
			defer cancelPoll()
			quiesced := false
			for attempt := 0; attempt < 20; attempt++ {
				remote, getErr := s.upstream.GetSession(pollCtx, secret, x.UpstreamSessionID)
				if getErr != nil {
					_ = s.store.MarkCleanup(ctx, o, id, "retry_pending", sanitizeError(getErr.Error()))
					return getErr
				}
				if remote == nil {
					break
				}
				if upstreamSessionDeletable(remote) {
					quiesced = true
					break
				}
				select {
				case <-pollCtx.Done():
					break
				case <-time.After(250 * time.Millisecond):
				}
				if pollCtx.Err() != nil {
					break
				}
			}
			if !quiesced {
				quiesceErr := errors.New("upstream session did not become idle or terminal after cancellation")
				_ = s.store.MarkCleanup(ctx, o, id, "retry_pending", quiesceErr.Error())
				return quiesceErr
			}
		}
		if !upstreamDeleted {
			err = s.upstream.DeleteSession(ctx, secret, x.UpstreamSessionID)
		}
		if err != nil {
			_ = s.store.MarkCleanup(ctx, o, id, "retry_pending", err.Error())
			return err
		}
		if !upstreamDeleted {
			if err = s.store.MarkCleanup(ctx, o, id, "upstream_deleted", ""); err != nil {
				return err
			}
			upstreamDeleted = true
		}
	}
	if s.artifacts != nil && x.SandboxID != "" {
		if err = s.artifacts.Cleanup(ctx, o, id); err != nil {
			status := "retry_pending"
			if upstreamDeleted {
				status = "upstream_deleted"
			}
			_ = s.store.MarkCleanup(ctx, o, id, status, sanitizeError(err.Error()))
			return err
		}
	}
	if x.SandboxID != "" && s.environments != nil {
		if err = s.environments.DeleteReference(ctx, o, id, x.SandboxID); err != nil {
			status := "retry_pending"
			if upstreamDeleted {
				status = "upstream_deleted"
			}
			_ = s.store.MarkCleanup(ctx, o, id, status, err.Error())
			return err
		}
	}
	return s.store.MarkCleanup(ctx, o, id, "deleted", "")
}

func isUpstreamNotFound(err error) bool {
	if err == nil {
		return false
	}
	var upstreamErr *upstreampkg.Error
	return errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusNotFound
}

func upstreamSessionDeletable(session *UpstreamSession) bool {
	if session == nil || session.HasRequiredActions {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(session.Status))
	return status == "idle" || status == "failed"
}
func (s *Service) RecordEvent(ctx context.Context, o Ownership, id string, e UpstreamEvent) (*Event, bool, error) {
	if err := authorize(o, "session:write"); err != nil {
		return nil, false, err
	}
	if e.ID == "" || e.Type == "" {
		return nil, false, ErrInvalid
	}
	stored, inserted, err := s.store.PutEvent(ctx, o, id, e)
	if err == nil && inserted {
		err = s.observeUsage(ctx, o, id, stored)
	}
	return stored, inserted, err
}
func (s *Service) Usage(ctx context.Context, o Ownership, id string) (usagepkg.UsageSnapshot, error) {
	if err := authorize(o, "session:read"); err != nil {
		return usagepkg.UsageSnapshot{}, err
	}
	if _, err := s.store.GetSession(ctx, o, id); err != nil {
		return usagepkg.UsageSnapshot{}, err
	}
	if s.usage == nil {
		return usagepkg.Unavailable(), nil
	}
	return s.usage.Snapshot(ctx, usagepkg.Scope{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID, LocalSessionID: id})
}
func (s *Service) observeUsage(ctx context.Context, o Ownership, id string, stored *Event) error {
	if s.usage == nil || stored == nil {
		return nil
	}
	x, err := s.store.GetSession(ctx, o, id)
	if err != nil {
		return err
	}
	_, err = s.usage.Observe(ctx, usagepkg.Event{Scope: usagepkg.Scope{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID, LocalSessionID: id}, UpstreamSessionID: x.UpstreamSessionID, EventID: stored.EventID, Cursor: stored.Cursor, ObservedAt: stored.CreatedAt, Payload: stored.Payload})
	return err
}
func (s *Service) ListEvents(ctx context.Context, o Ownership, id string, after int64, limit int) ([]Event, error) {
	if err := authorize(o, "session:read"); err != nil {
		return nil, err
	}
	if _, err := s.store.GetSession(ctx, o, id); err != nil {
		return nil, err
	}
	return s.store.ListEvents(ctx, o, id, after, limit)
}
func (s *Service) connectionSecret(ctx context.Context, o Ownership, id string) (*Connection, ConnectionSecret, error) {
	c, err := s.store.GetConnection(ctx, o, id)
	if err != nil {
		return nil, ConnectionSecret{}, err
	}
	secret, err := s.decryptSecret(c.credentialRef)
	secret.ProjectID = c.ProjectID
	secret.OrganizationID = c.OpenAIOrganization
	return c, secret, err
}
func (s *Service) sessionSecret(ctx context.Context, o Ownership, id string) (*Session, ConnectionSecret, error) {
	x, err := s.store.GetSession(ctx, o, id)
	if err != nil {
		return nil, ConnectionSecret{}, err
	}
	_, secret, err := s.connectionSecret(ctx, o, x.ConnectionID)
	return x, secret, err
}
func (s *Service) encryptSecret(v ConnectionSecret) (string, error) {
	b, _ := json.Marshal(v)
	return s.cipher.Encrypt(string(b))
}
func (s *Service) decryptSecret(v string) (ConnectionSecret, error) {
	plain, err := s.cipher.Decrypt(v)
	if err != nil {
		return ConnectionSecret{}, err
	}
	var out ConnectionSecret
	if err = json.Unmarshal([]byte(plain), &out); err != nil {
		return out, fmt.Errorf("decode encrypted credential")
	}
	return out, nil
}
func isAmbiguous(err error) bool {
	var a AmbiguousError
	return errors.As(err, &a) && a.AmbiguousResult()
}

var secretPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]+|bearer\s+[A-Za-z0-9._~-]+|https?://[^\s"']+)`)

func sanitizeError(v string) string {
	v = secretPattern.ReplaceAllString(v, "[redacted]")
	if len(v) > 1024 {
		v = v[:1024]
	}
	return v
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *Service) StartRecovery(ctx context.Context) error {
	rs, ok := s.store.(RecoveryStore)
	if !ok {
		return ErrUnavailable
	}
	records, err := rs.ListRecoverable(ctx)
	if err != nil {
		return err
	}
	for _, r := range records {
		if s.recoveryAuth == nil {
			_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", "background authorization is unavailable")
			continue
		}
		refreshed, authErr := s.recoveryAuth.AuthorizeRecovery(ctx, r.Ownership)
		if authErr != nil || authorize(refreshed, "session:write") != nil {
			_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", "session owner authorization was revoked")
			continue
		}
		r.Ownership = refreshed
		_, secret, secretErr := s.connectionSecret(ctx, r.Ownership, r.Session.ConnectionID)
		if secretErr != nil {
			_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", sanitizeError(secretErr.Error()))
			continue
		}
		owned, _, leaseErr := s.acquireSessionOwnership(ctx, r.Ownership, r.Session.ID)
		if leaseErr != nil {
			return leaseErr
		}
		if !owned {
			continue
		}
		s.startSupervisor(r.Ownership, r.Session.ID, r.Session.UpstreamSessionID)
		if r.Session.Environment.Type == "self_hosted" && r.Session.SandboxID != "" && s.environments != nil {
			remote, remoteErr := s.upstream.GetSession(ctx, secret, r.Session.UpstreamSessionID)
			if remoteErr != nil || remote == nil {
				if remoteErr == nil {
					remoteErr = ErrUnavailable
				}
				_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", sanitizeError(remoteErr.Error()))
				continue
			}
			c, _, credentialErr := s.connectionSecret(ctx, r.Ownership, r.Session.ConnectionID)
			if credentialErr != nil || c.executorRef == "" {
				if credentialErr == nil {
					credentialErr = ErrUnavailable
				}
				_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", sanitizeError(credentialErr.Error()))
				continue
			}
			executorSecret, decryptErr := s.decryptSecret(c.executorRef)
			if decryptErr != nil {
				_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", sanitizeError(decryptErr.Error()))
				continue
			}
			if strings.TrimSpace(executorSecret.ExecutorKey) == strings.TrimSpace(secret.APIKey) {
				_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", "executor credential matches application credential")
				continue
			}
			env, reconcileErr := s.environments.Reconcile(ownedContext(ctx, r.Ownership), EnvironmentReconcileRequest{Ownership: r.Ownership, LocalSessionID: r.Session.ID, UpstreamEnvironmentID: r.Session.UpstreamEnvironmentID, SandboxID: r.Session.SandboxID, RemoteURL: remote.RemoteURL, ExecutorAPIKey: executorSecret.ExecutorKey, Status: r.Session.Readiness, Lifetime: s.sessionOwnershipContext(r.Session.ID), FenceStartupTimeout: s.environmentTimeoutFence(r.Ownership, r.Session.ID)})
			if reconcileErr != nil {
				_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, "recovery_pending", "failed", sanitizeError(reconcileErr.Error()))
				continue
			}
			if env != nil {
				_ = s.store.UpdateSessionState(ctx, r.Ownership, r.Session.ID, r.Session.Status, env.Status, env.Error)
			}
		}
	}
	return nil
}
func (s *Service) startSupervisor(o Ownership, localID string, upstreamID string) {
	source, ok := s.upstream.(EventSource)
	if !ok || upstreamID == "" {
		return
	}
	owned, _, err := s.acquireSessionOwnership(s.supervisorCtx, o, localID)
	if err != nil || !owned {
		return
	}
	s.supervisorMu.Lock()
	if s.closing {
		s.supervisorMu.Unlock()
		return
	}
	if _, exists := s.supervising[localID]; exists {
		s.supervisorMu.Unlock()
		return
	}
	sessionCtx, sessionCancel := context.WithCancel(s.supervisorCtx)
	s.supervising[localID] = sessionCancel
	s.supervisorWG.Add(1)
	s.supervisorMu.Unlock()
	go func() {
		defer s.supervisorWG.Done()
		defer func() { s.supervisorMu.Lock(); delete(s.supervising, localID); s.supervisorMu.Unlock() }()
		supervisorContext := ownedContext(sessionCtx, o)
		for {
			if s.recoveryAuth != nil {
				refreshed, authErr := s.recoveryAuth.AuthorizeRecovery(supervisorContext, o)
				if authErr != nil || authorize(refreshed, "session:write") != nil {
					_ = s.store.UpdateSessionState(supervisorContext, o, localID, "recovery_pending", "failed", "session owner authorization was revoked")
					return
				}
				o = refreshed
				supervisorContext = ownedContext(sessionCtx, o)
			}
			_, secret, secretErr := s.sessionSecret(supervisorContext, o, localID)
			if secretErr != nil {
				return
			}
			err := source.StreamEvents(supervisorContext, secret, upstreamID, func(e UpstreamEvent) error {
				if err := supervisorContext.Err(); err != nil {
					return err
				}
				stored, inserted, putErr := s.store.PutEvent(supervisorContext, o, localID, e)
				if putErr != nil {
					return putErr
				}
				if inserted {
					if putErr = s.observeUsage(supervisorContext, o, localID, stored); putErr != nil {
						return putErr
					}
				}
				if readinessErr := s.observeEnvironmentEvent(supervisorContext, o, localID, e, inserted); readinessErr != nil {
					return readinessErr
				}
				if s.tools != nil {
					if strings.Contains(strings.ToLower(e.Type), "required_action") && s.recoveryAuth != nil {
						refreshed, authErr := s.recoveryAuth.AuthorizeRecovery(supervisorContext, o)
						if authErr != nil || authorize(refreshed, "session:write") != nil {
							return ErrForbidden
						}
						o = refreshed
						supervisorContext = ownedContext(sessionCtx, o)
					}
					results, toolErr := s.tools.HandleRequiredActions(supervisorContext, o, localID, e)
					if toolErr != nil {
						return toolErr
					}
					for i := range results {
						result := &results[i]
						if result.State == "pending" {
							continue
						}
						submitted, submitErr := s.submitToolResult(supervisorContext, o, localID, secret, upstreamID, *result)
						if submitErr != nil {
							return submitErr
						}
						if !submitted {
							continue
						}
					}
				}
				switch strings.ToLower(e.Type) {
				case "agent.session.failed":
					if stateErr := s.store.UpdateSessionState(supervisorContext, o, localID, "failed", "pending", ""); stateErr != nil {
						return stateErr
					}
				case "agent.session.cancelled", "agent.session.canceled":
					if stateErr := s.store.UpdateSessionState(supervisorContext, o, localID, "canceled", "pending", ""); stateErr != nil {
						return stateErr
					}
				}
				return nil
			})
			if err == nil || errors.Is(err, context.Canceled) || supervisorContext.Err() != nil {
				return
			}
			_ = s.store.UpdateSessionState(supervisorContext, o, localID, "recovery_pending", "pending", sanitizeError(err.Error()))
			select {
			case <-supervisorContext.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
}

var errEnvironmentNotBound = errors.New("environment has no local executor binding")

func (s *Service) observeEnvironmentEvent(ctx context.Context, o Ownership, localID string, e UpstreamEvent, inserted bool) error {
	// Stream reconciliation can replay durable events after a replacement
	// executor is installed. Readiness side effects therefore apply only once.
	if !inserted || s.environments == nil {
		return nil
	}
	var envStatus string
	switch strings.ToLower(e.Type) {
	case "agent.session.environment.connected":
		envStatus = "connected"
	case "agent.session.environment.failed":
		envStatus = "failed"
	case "agent.session.environment.disconnected", "agent.session.environment.pending":
		envStatus = "pending"
	default:
		return nil
	}
	// A provider disconnect is durable evidence that the environment is no
	// longer ready, even after Relinquish has removed the local binding.
	if envStatus == "pending" {
		current, err := s.store.GetSession(ctx, o, localID)
		if err != nil {
			return err
		}
		if err = s.store.UpdateSessionState(ctx, o, localID, current.Status, "pending", ""); err != nil {
			return err
		}
	}
	env, err := s.environments.Observe(ctx, EnvironmentObserveRequest{Ownership: o, LocalSessionID: localID, Status: envStatus, Error: func() string {
		if envStatus == "failed" {
			return "upstream environment failed"
		}
		return ""
	}()})
	if err != nil {
		if errors.Is(err, errEnvironmentNotBound) {
			return nil
		}
		return err
	}
	if env == nil {
		return nil
	}
	current, currentErr := s.store.GetSession(ctx, o, localID)
	if currentErr != nil {
		return currentErr
	}
	sessionStatus := current.Status
	if envStatus == "failed" {
		sessionStatus = "failed"
	}
	return s.store.UpdateSessionState(ctx, o, localID, sessionStatus, env.Status, env.Error)
}
func (s *Service) stopSupervisor(ctx context.Context, id string) error {
	s.supervisorMu.Lock()
	cancel := s.supervising[id]
	s.supervisorMu.Unlock()
	if cancel != nil {
		cancel()
		for {
			s.supervisorMu.Lock()
			_, running := s.supervising[id]
			s.supervisorMu.Unlock()
			if !running {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return nil
}
func ownedContext(ctx context.Context, o Ownership) context.Context {
	ctx = contextkeys.WithUserID(ctx, o.PrincipalID)
	ctx = contextkeys.WithUserRole(ctx, o.Role)
	ctx = contextkeys.WithTenantID(ctx, o.InstanceID)
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{InstanceID: o.InstanceID, OrganizationID: o.OrganizationID})
	return database.WithTenantSchema(ctx, o.InstanceID)
}
func sanitizeJSON(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return json.RawMessage(`{}`)
	}
	var v any
	if json.Unmarshal(in, &v) != nil {
		return json.RawMessage(`{"redacted":true}`)
	}
	redactJSON(v)
	out, _ := json.Marshal(v)
	return out
}
func redactJSON(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			l := strings.ToLower(k)
			if (l == "input_tokens" || l == "output_tokens" || l == "total_tokens") && isJSONNumber(val) {
				continue
			}
			if strings.Contains(l, "key") || strings.Contains(l, "secret") || strings.Contains(l, "token") || strings.Contains(l, "remote_url") {
				x[k] = "[redacted]"
			} else if text, ok := val.(string); ok {
				x[k] = sanitizeError(text)
			} else {
				redactJSON(val)
			}
		}
	case []any:
		for _, val := range x {
			redactJSON(val)
		}
	}
}
func isJSONNumber(v any) bool { _, ok := v.(float64); return ok }
