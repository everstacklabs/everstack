package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/everstacklabs/everstack/internal/functions/executor"
	functionsquery "github.com/everstacklabs/everstack/internal/query/handlers/functions"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// FunctionAdapter executes through the existing function executor registry.
// The supplied registry should contain the same webhook, proxy and isolated
// executors as the native tool loop.
type FunctionAdapter struct {
	db       *sqlx.DB
	registry *executor.Registry
}

func NewFunctionAdapter(db *sqlx.DB, registry *executor.Registry) (*FunctionAdapter, error) {
	if db == nil || registry == nil {
		return nil, errors.New("function database and executor registry are required")
	}
	return &FunctionAdapter{db: db, registry: registry}, nil
}

func (a *FunctionAdapter) function(ctx context.Context, o Owner, id, name string) (*functionsquery.FunctionReadModel, error) {
	var fn functionsquery.FunctionReadModel
	err := a.db.GetContext(ctx, &fn, `SELECT * FROM functions WHERE id=$1 AND tenant_id=$2 AND name=$3 AND enabled=true`, id, o.InstanceID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDenied
	}
	if err != nil {
		return nil, err
	}
	if fn.Mode != string(executor.ModeWebhook) && fn.Mode != string(executor.ModeProxy) {
		return nil, errors.New("function execution mode is unavailable for OpenAI Agents")
	}
	return &fn, nil
}

func (a *FunctionAdapter) ResolveFunction(ctx context.Context, o Owner, id, name string) (Grant, error) {
	fn, err := a.function(ctx, o, id, name)
	if err != nil {
		return Grant{}, err
	}
	if fn.Mode != string(executor.ModeWebhook) && fn.Mode != string(executor.ModeProxy) {
		return Grant{}, errors.New("function execution mode is unavailable for OpenAI Agents")
	}
	params := map[string]any{}
	if len(fn.Parameters) > 0 && json.Unmarshal(fn.Parameters, &params) != nil {
		return Grant{}, errors.New("invalid function parameter schema")
	}
	description := ""
	if fn.Description.Valid {
		description = fn.Description.String
	}
	return Grant{Kind: "function", ResourceID: fn.ID, Name: fn.Name, Description: description, Parameters: params}, nil
}

func (a *FunctionAdapter) ExecuteFunction(ctx context.Context, o Owner, g Grant, args map[string]any) (any, error) {
	fn, err := a.function(ctx, o, g.ResourceID, g.Name)
	if err != nil {
		return nil, err
	}
	config := functionConfig(fn)
	// Provider-required-action delivery can repeat after ambiguous failures.
	// Durable call claiming is the retry boundary, so executor retries stay off.
	config.MaxRetries = 0
	result, err := a.registry.Execute(ctx, &executor.ExecutionContext{RequestID: uuid.NewString(), TenantID: o.InstanceID, UserID: o.PrincipalID}, config, &executor.ToolCall{ID: uuid.NewString(), Name: fn.Name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Success {
		return nil, errors.New("function execution failed")
	}
	return result.Content, nil
}

func functionConfig(fn *functionsquery.FunctionReadModel) *executor.FunctionConfig {
	c := &executor.FunctionConfig{ID: fn.ID, TenantID: fn.TenantID, Name: fn.Name, Mode: executor.ExecutionMode(fn.Mode), TimeoutMs: fn.TimeoutMs}
	decode(fn.Parameters, &c.Parameters)
	if fn.WebhookURL.Valid {
		c.WebhookURL = fn.WebhookURL.String
	}
	if fn.WebhookMethod.Valid {
		c.WebhookMethod = fn.WebhookMethod.String
	}
	if fn.WebhookTimeoutMs.Valid {
		c.WebhookTimeoutMs = fn.WebhookTimeoutMs.Int32
	}
	decode(fn.WebhookHeaders, &c.WebhookHeaders)
	if fn.ProxyBaseURL.Valid {
		c.ProxyBaseURL = fn.ProxyBaseURL.String
	}
	if fn.ProxyPath.Valid {
		c.ProxyPath = fn.ProxyPath.String
	}
	if fn.ProxyMethod.Valid {
		c.ProxyMethod = fn.ProxyMethod.String
	}
	decode(fn.ProxyQueryMapping, &c.ProxyQueryMapping)
	decode(fn.ProxyHeaderMapping, &c.ProxyHeaderMapping)
	decode(fn.ProxyBodyMapping, &c.ProxyBodyMapping)
	decode(fn.ProxyResponseMapping, &c.ProxyResponseMapping)
	if fn.Runtime.Valid {
		c.Runtime = fn.Runtime.String
	}
	if fn.Code.Valid {
		c.Code = fn.Code.String
	}
	c.Packages = fn.Packages
	c.NetworkMode = "deny"
	if fn.NetworkMode.Valid {
		c.NetworkMode = fn.NetworkMode.String
	}
	c.AllowedHosts = fn.AllowedHosts
	c.VCPUs = int(fn.VCPUs)
	if c.VCPUs == 0 {
		c.VCPUs = 1
	}
	c.MemoryMB = int(fn.MemoryMB)
	if c.MemoryMB == 0 {
		c.MemoryMB = 512
	}
	if fn.DockerHost.Valid {
		c.DockerHost = fn.DockerHost.String
	}
	return c
}
func decode(b []byte, out any) {
	if len(b) > 0 {
		_ = json.Unmarshal(b, out)
	}
}
