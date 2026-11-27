package interop

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	a2a "github.com/everstacklabs/everstack/internal/a2a/client"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("interop: not found")
var ErrConflict = errors.New("interop: idempotency key already used for a different request")
var ErrInvalid = errors.New("interop: invalid request")
var ErrBusy = errors.New("interop: run operation already in progress")
var ErrRemoteChanged = errors.New("interop: remote connection changed or was removed")
var ErrNoTask = errors.New("interop: remote did not provide a task handle; do not resend")

type ExternalRun struct {
	ID                string     `db:"id" json:"id"`
	TenantID          string     `db:"tenant_id" json:"-"`
	RemoteID          string     `db:"remote_id" json:"remote_id"`
	RemoteName        string     `db:"remote_name" json:"remote_name"`
	Endpoint          string     `db:"endpoint" json:"-"`
	Protocol          string     `db:"protocol" json:"protocol"`
	RoutingTenant     string     `db:"routing_tenant" json:"-"`
	IdempotencyKey    string     `db:"idempotency_key" json:"-"`
	RequestHash       string     `db:"request_hash" json:"-"`
	State             string     `db:"state" json:"state"`
	RemoteTaskID      string     `db:"remote_task_id" json:"remote_task_id,omitempty"`
	RemoteContextID   string     `db:"remote_context_id" json:"remote_context_id,omitempty"`
	Output            string     `db:"output" json:"output"`
	LastError         string     `db:"last_error" json:"last_error,omitempty"`
	CancelRequestedAt *time.Time `db:"cancel_requested_at" json:"cancel_requested_at,omitempty"`
	LeaseToken        string     `db:"lease_token" json:"-"`
	LeaseUntil        time.Time  `db:"lease_until" json:"-"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
}

type RunRepository interface {
	GetRemoteAgentByName(context.Context, string, string) (*RemoteAgent, error)
	GetRemoteAgentByID(context.Context, string, string) (*RemoteAgent, error)
	CreateRun(context.Context, ExternalRun) (ExternalRun, bool, error)
	GetRun(context.Context, string, string) (ExternalRun, error)
	ListRuns(context.Context, string) ([]ExternalRun, error)
	ClaimRun(context.Context, string, string, bool) (ExternalRun, error)
	FinishRun(context.Context, ExternalRun) (ExternalRun, error)
}
type AgentClient interface {
	Send(context.Context, a2a.Connection, string, string) (a2a.Result, error)
	Get(context.Context, a2a.Connection, string) (a2a.Result, error)
	Cancel(context.Context, a2a.Connection, string) (a2a.Result, error)
}
type RunService struct {
	Repository RunRepository
	Client     AgentClient
}

func remoteConnection(r *RemoteAgent) a2a.Connection {
	return a2a.Connection{Endpoint: r.Endpoint, Protocol: r.Protocol, Token: r.AuthToken, Tenant: r.RoutingTenant}
}
func (s *RunService) Start(ctx context.Context, tenant, remote, message, key string) (ExternalRun, error) {
	if tenant == "" {
		return ExternalRun{}, ErrNoTenant
	}
	if strings.TrimSpace(message) == "" || len(message) > 64<<10 || len(key) < 8 || len(key) > 128 || strings.TrimSpace(remote) == "" {
		return ExternalRun{}, ErrInvalid
	}
	connection, err := s.Repository.GetRemoteAgentByName(ctx, tenant, remote)
	if err != nil {
		return ExternalRun{}, err
	}
	if connection == nil {
		return ExternalRun{}, ErrNotFound
	}
	if err = a2a.Validate(remoteConnection(connection)); err != nil {
		return ExternalRun{}, err
	}
	digest := sha256.Sum256([]byte(connection.ID + "\x00" + message))
	run := ExternalRun{ID: uuid.NewString(), TenantID: tenant, RemoteID: connection.ID, RemoteName: connection.Name, Endpoint: connection.Endpoint, Protocol: connection.Protocol, RoutingTenant: connection.RoutingTenant, IdempotencyKey: key, RequestHash: hex.EncodeToString(digest[:]), State: "dispatching", LeaseToken: uuid.NewString()}
	run, created, err := s.Repository.CreateRun(ctx, run)
	if err != nil || !created {
		return run, err
	}
	result, remoteErr := s.Client.Send(ctx, remoteConnection(connection), message, run.ID)
	if remoteErr != nil {
		// Only a definite refusal is terminal. Anything else may have been
		// accepted, so it is recorded as unknown and never resent.
		run.State = "unknown"
		if a2a.Rejected(remoteErr) {
			run.State = "rejected"
		}
		run.LastError = a2a.ErrorCode(remoteErr)
		// A response with a usable handle can still contain unsupported content.
		// Retain that handle for reconciliation without declaring completion.
		if result.TaskID != "" {
			run.RemoteTaskID = result.TaskID
			run.RemoteContextID = result.ContextID
		}
	} else {
		applyResult(&run, result)
	}
	return s.finish(ctx, run)
}
func (s *RunService) Read(ctx context.Context, tenant, id string) (ExternalRun, error) {
	if tenant == "" {
		return ExternalRun{}, ErrNoTenant
	}
	if _, err := uuid.Parse(id); err != nil {
		return ExternalRun{}, ErrNotFound
	}
	return s.Repository.GetRun(ctx, tenant, id)
}
func (s *RunService) Refresh(ctx context.Context, tenant, id string, cancel bool) (ExternalRun, error) {
	run, err := s.Read(ctx, tenant, id)
	if err != nil {
		return run, err
	}
	if a2a.Terminal(run.State) {
		return run, nil
	}
	if run.RemoteTaskID == "" {
		return run, ErrNoTask
	}
	connection, err := s.Repository.GetRemoteAgentByID(ctx, tenant, run.RemoteID)
	if err != nil {
		return run, err
	}
	if connection == nil || connection.Endpoint != run.Endpoint || connection.Protocol != run.Protocol || connection.RoutingTenant != run.RoutingTenant {
		return run, ErrRemoteChanged
	}
	run, err = s.Repository.ClaimRun(ctx, tenant, id, cancel)
	if err != nil {
		return run, err
	}
	var result a2a.Result
	if cancel {
		result, err = s.Client.Cancel(ctx, remoteConnection(connection), run.RemoteTaskID)
	} else {
		result, err = s.Client.Get(ctx, remoteConnection(connection), run.RemoteTaskID)
	}
	if err != nil {
		run.LastError = a2a.ErrorCode(err)
	} else if result.TaskID != run.RemoteTaskID || (run.RemoteContextID != "" && result.ContextID != "" && result.ContextID != run.RemoteContextID) {
		run.LastError = "task_identity_changed"
	} else if run.State == "working" && result.State == "submitted" {
		run.LastError = "task_state_regressed"
	} else {
		applyResult(&run, result)
	}
	return s.finish(ctx, run)
}
func applyResult(run *ExternalRun, result a2a.Result) {
	run.State = result.State
	run.RemoteTaskID = result.TaskID
	if result.ContextID != "" {
		run.RemoteContextID = result.ContextID
	}
	if result.Text != "" || run.Output == "" {
		run.Output = result.Text
	}
	run.LastError = ""
}
func (s *RunService) finish(ctx context.Context, run ExternalRun) (ExternalRun, error) {
	// A disconnected caller must not erase a remote acceptance. Persist only;
	// never detach and retry network execution after cancellation.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.Repository.FinishRun(saveCtx, run)
}

// Leases are 150s: the client's 120s bound, finish's 10s save, and slack so
// a call that returns at its deadline is not marked interrupted first.
const runColumns = `id,tenant_id,remote_id,remote_name,endpoint,protocol,routing_tenant,idempotency_key,request_hash,state,remote_task_id,remote_context_id,output,last_error,cancel_requested_at,lease_token,lease_until,created_at,updated_at`

func (s *Store) CreateRun(ctx context.Context, run ExternalRun) (ExternalRun, bool, error) {
	if run.TenantID == "" {
		return ExternalRun{}, false, ErrNoTenant
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO a2a_external_runs (id,tenant_id,remote_id,remote_name,endpoint,protocol,routing_tenant,idempotency_key,request_hash,state,lease_token,lease_until) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'dispatching',$10,NOW()+INTERVAL '150 seconds') ON CONFLICT (tenant_id,idempotency_key) DO NOTHING`, run.ID, run.TenantID, run.RemoteID, run.RemoteName, run.Endpoint, run.Protocol, run.RoutingTenant, run.IdempotencyKey, run.RequestHash, run.LeaseToken)
	if err != nil {
		return ExternalRun{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ExternalRun{}, false, err
	}
	var saved ExternalRun
	err = s.db.GetContext(ctx, &saved, `SELECT `+runColumns+` FROM a2a_external_runs WHERE tenant_id=$1 AND idempotency_key=$2`, run.TenantID, run.IdempotencyKey)
	if err == nil && saved.RequestHash != run.RequestHash {
		return ExternalRun{}, false, ErrConflict
	}
	return saved, count == 1, err
}
func (s *Store) GetRun(ctx context.Context, tenant, id string) (ExternalRun, error) {
	if tenant == "" {
		return ExternalRun{}, ErrNoTenant
	}
	// An expired dispatch lease is evidence of uncertainty, never permission to
	// send the original message again. Poll/cancel leases keep the observed state.
	_, err := s.db.ExecContext(ctx, `UPDATE a2a_external_runs SET state='unknown',last_error='dispatch_interrupted',lease_token='',updated_at=NOW() WHERE tenant_id=$1 AND id=$2 AND state='dispatching' AND lease_until<NOW()`, tenant, id)
	if err != nil {
		return ExternalRun{}, err
	}
	var run ExternalRun
	err = s.db.GetContext(ctx, &run, `SELECT `+runColumns+` FROM a2a_external_runs WHERE tenant_id=$1 AND id=$2`, tenant, id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return run, err
}
func (s *Store) ListRuns(ctx context.Context, tenant string) ([]ExternalRun, error) {
	if tenant == "" {
		return nil, ErrNoTenant
	}
	_, err := s.db.ExecContext(ctx, `UPDATE a2a_external_runs SET state='unknown',last_error='dispatch_interrupted',lease_token='',updated_at=NOW() WHERE tenant_id=$1 AND state='dispatching' AND lease_until<NOW()`, tenant)
	if err != nil {
		return nil, err
	}
	runs := []ExternalRun{}
	err = s.db.SelectContext(ctx, &runs, `SELECT `+strings.Replace(runColumns, "output,", "LEFT(output,4096) AS output,", 1)+` FROM a2a_external_runs WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 50`, tenant)
	return runs, err
}
func (s *Store) ClaimRun(ctx context.Context, tenant, id string, cancel bool) (ExternalRun, error) {
	if tenant == "" {
		return ExternalRun{}, ErrNoTenant
	}
	var run ExternalRun
	err := s.db.GetContext(ctx, &run, `UPDATE a2a_external_runs SET lease_token=$3,lease_until=NOW()+INTERVAL '150 seconds',cancel_requested_at=CASE WHEN $4 THEN NOW() ELSE cancel_requested_at END WHERE tenant_id=$1 AND id=$2 AND lease_until<NOW() AND state NOT IN ('completed','failed','canceled','rejected') RETURNING `+runColumns, tenant, id, uuid.NewString(), cancel)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrBusy
	}
	return run, err
}
func (s *Store) FinishRun(ctx context.Context, run ExternalRun) (ExternalRun, error) {
	if run.TenantID == "" {
		return ExternalRun{}, ErrNoTenant
	}
	if run.LeaseToken == "" {
		return ExternalRun{}, ErrBusy
	}
	var saved ExternalRun
	err := s.db.GetContext(ctx, &saved, `UPDATE a2a_external_runs SET state=$4,remote_task_id=$5,remote_context_id=$6,output=$7,last_error=$8,lease_token='',lease_until=NOW(),updated_at=NOW() WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 RETURNING `+runColumns, run.TenantID, run.ID, run.LeaseToken, run.State, run.RemoteTaskID, run.RemoteContextID, run.Output, run.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrBusy
	}
	return saved, err
}
