package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("openai agents resource not found")
	ErrUnavailable   = errors.New("openai agents durable store unavailable")
	ErrForbidden     = errors.New("openai agents action forbidden")
	ErrInvalid       = errors.New("invalid openai agents request")
	ErrCreatePending = errors.New("openai agents session creation outcome pending recovery")
	// ErrConnectionInUse guards the one destructive path that cannot be undone.
	// Deleting a connection clears credential_ref and executor_credential_ref,
	// and those are the only credentials that can tear a session down upstream
	// or stop its sandbox. Allowing the delete while sessions still hold
	// resources strands a running VM that nothing can reach or destroy, billed
	// to the tenant. Refuse instead, and let the caller retire the sessions.
	ErrConnectionInUse = errors.New("openai agents connection still has sessions that hold resources")
)

type Store interface {
	CreateConnection(context.Context, Ownership, Connection, string, string) (*Connection, error)
	ListConnections(context.Context, Ownership) ([]Connection, error)
	GetConnection(context.Context, Ownership, string) (*Connection, error)
	UpdateConnection(context.Context, Ownership, Connection, string, string) (*Connection, error)
	UpdateConnectionStatus(context.Context, Ownership, string, string, string) error
	DeleteConnection(context.Context, Ownership, string) error
	BeginSessionCreate(context.Context, Ownership, Session) (*Session, bool, error)
	CompleteSessionCreate(context.Context, Ownership, string, UpstreamSession) (*Session, error)
	BindEnvironment(context.Context, Ownership, string, string, string) error
	MarkSessionCreateAmbiguous(context.Context, Ownership, string, string) error
	ListSessions(context.Context, Ownership) ([]Session, error)
	GetSession(context.Context, Ownership, string) (*Session, error)
	UpdateSessionState(context.Context, Ownership, string, string, string, string) error
	UpdateSessionStatus(context.Context, Ownership, string, string, string) error
	MarkCleanup(context.Context, Ownership, string, string, string) error
	PutEvent(context.Context, Ownership, string, UpstreamEvent) (*Event, bool, error)
	ListEvents(context.Context, Ownership, string, int64, int) ([]Event, error)
	ClaimInput(context.Context, Ownership, string, string, string) (bool, error)
	InputStatus(context.Context, Ownership, string, string) (string, string, error)
	CompleteInput(context.Context, Ownership, string, string, string) error
}
type RecoveryRecord struct {
	Ownership Ownership
	Session   Session
}
type RecoveryStore interface {
	ListRecoverable(context.Context) ([]RecoveryRecord, error)
}
type SessionLeaseStore interface {
	AcquireSessionLease(context.Context, Ownership, string, string, time.Duration) (bool, error)
	RenewSessionLease(context.Context, Ownership, string, string, time.Duration) (bool, error)
	ReleaseSessionLease(context.Context, Ownership, string, string) error
}
type EnvironmentCleanupFenceStore interface {
	FenceEnvironmentTimeout(context.Context, Ownership, string, string, string) (bool, error)
}
type RecoveryAuthorizer interface {
	AuthorizeRecovery(context.Context, Ownership) (Ownership, error)
}

type Cipher interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

type Upstream interface {
	ValidateConnection(context.Context, ConnectionSecret) error
	CreateSession(context.Context, ConnectionSecret, CreateSessionRequest) (*UpstreamSession, error)
	RecoverSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error)
	GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error)
	SendInput(context.Context, ConnectionSecret, string, InputRequest) error
	CancelSession(context.Context, ConnectionSecret, string) error
	DeleteSession(context.Context, ConnectionSecret, string) error
}
type EventSource interface {
	StreamEvents(context.Context, ConnectionSecret, string, func(UpstreamEvent) error) error
}
type ToolResultSubmitter interface {
	SubmitToolResult(context.Context, ConnectionSecret, string, ToolResult) error
}
type ToolResultAcceptanceChecker interface {
	ToolResultAccepted(context.Context, ConnectionSecret, string, ToolResult) (bool, error)
}

type AmbiguousError interface {
	error
	AmbiguousResult() bool
}

type EnvironmentManager interface {
	ValidateConfiguration(context.Context) error
	Provision(context.Context, EnvironmentProvisionRequest) (*EnvironmentState, error)
	Reconcile(context.Context, EnvironmentReconcileRequest) (*EnvironmentState, error)
	Observe(context.Context, EnvironmentObserveRequest) (*EnvironmentState, error)
	Relinquish(context.Context, Ownership, string) error
	DeleteReference(context.Context, Ownership, string, string) error
}
type EnvironmentObserveRequest struct {
	Ownership
	LocalSessionID, Status, Error string
}
type EnvironmentReconcileRequest struct {
	Ownership
	LocalSessionID, UpstreamEnvironmentID, SandboxID, RemoteURL, ExecutorAPIKey string
	Status                                                                      string
	Lifetime                                                                    context.Context
	FenceStartupTimeout                                                         func(context.Context) error
}

type EnvironmentProvisionRequest struct {
	Ownership
	LocalSessionID        string
	UpstreamEnvironmentID string
	RemoteURL             string
	ExecutorAPIKey        string
	WorkspaceDirectory    string
	Image                 string
	Lifetime              context.Context
	FenceStartupTimeout   func(context.Context) error
}

type EnvironmentState struct {
	SandboxID string
	Status    string
	Error     string
}

type ResolvedTools struct {
	Configs []json.RawMessage
	binding any
}
type ToolController interface {
	ResolveTools(context.Context, Ownership, []ToolSpec) (ResolvedTools, error)
	BindSession(context.Context, Ownership, string, ResolvedTools) error
	HandleRequiredActions(context.Context, Ownership, string, UpstreamEvent) ([]ToolResult, error)
}
type ToolDeliveryController interface {
	ClaimToolResultDelivery(context.Context, Ownership, string, ToolResult, string, time.Duration) (bool, string, error)
	CompleteToolResultDelivery(context.Context, Ownership, string, ToolResult, string) error
	MarkToolResultDeliveryUncertain(context.Context, Ownership, string, ToolResult, string) error
	ReleaseToolResultDelivery(context.Context, Ownership, string, ToolResult, string) error
}
type ApprovalController interface {
	ListPending(context.Context, Ownership) ([]Approval, error)
	Review(context.Context, Ownership, string) (Approval, error)
	Resolve(context.Context, Ownership, string, string) error
}
