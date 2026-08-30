package functions

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// WebhookConfig defines webhook execution settings
type WebhookConfig struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	TimeoutMs int32             `json:"timeout_ms"`
}

// ProxyConfig defines proxy execution settings
type ProxyConfig struct {
	BaseURL         string            `json:"base_url"`
	Path            string            `json:"path"`
	Method          string            `json:"method"`
	QueryMapping    map[string]string `json:"query_mapping"`
	HeaderMapping   map[string]string `json:"header_mapping"`
	BodyMapping     map[string]string `json:"body_mapping"`
	ResponseMapping map[string]string `json:"response_mapping"`
}

// IsolatedConfig defines isolated runtime execution settings (Phase 2)
type IsolatedConfig struct {
	Runtime    string   `json:"runtime"`
	Code       string   `json:"code"`
	Packages   []string `json:"packages"`
	DockerHost string   `json:"docker_host,omitempty"`
}

// CreateFunctionCommand creates a new function
type CreateFunctionCommand struct {
	commands.BaseCommand
	TenantID    string                 `json:"tenant_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Mode        string                 `json:"mode"` // webhook, proxy, isolated
	Parameters  map[string]interface{} `json:"parameters"`
	Webhook     *WebhookConfig         `json:"webhook,omitempty"`
	Proxy       *ProxyConfig           `json:"proxy,omitempty"`
	Isolated    *IsolatedConfig        `json:"isolated,omitempty"`
	TimeoutMs   int32                  `json:"timeout_ms"`
	MemoryMB    int32                  `json:"memory_mb"`
	MaxRetries  int32                  `json:"max_retries"`
}

func NewCreateFunctionCommand(
	tenantID, name, description, mode string,
	parameters map[string]interface{},
	webhook *WebhookConfig,
	proxy *ProxyConfig,
	isolated *IsolatedConfig,
	timeoutMs, memoryMB, maxRetries int32,
	userID, traceID string,
) *CreateFunctionCommand {
	return &CreateFunctionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:    tenantID,
		Name:        name,
		Description: description,
		Mode:        mode,
		Parameters:  parameters,
		Webhook:     webhook,
		Proxy:       proxy,
		Isolated:    isolated,
		TimeoutMs:   timeoutMs,
		MemoryMB:    memoryMB,
		MaxRetries:  maxRetries,
	}
}

func (c CreateFunctionCommand) AggregateID() string { return c.ID }
func (c CreateFunctionCommand) CommandType() string { return "CreateFunction" }
func (c CreateFunctionCommand) Validate() error {
	// tenant_id is optional for self-hosted mode - will use "default" if empty
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if c.Mode == "" {
		return fmt.Errorf("mode cannot be empty")
	}
	if c.Mode != "webhook" && c.Mode != "proxy" && c.Mode != "isolated" {
		return fmt.Errorf("invalid mode: %s (must be webhook, proxy, or isolated)", c.Mode)
	}
	if c.Mode == "webhook" && (c.Webhook == nil || c.Webhook.URL == "") {
		return fmt.Errorf("webhook config with URL is required for webhook mode")
	}
	if c.Mode == "proxy" && (c.Proxy == nil || c.Proxy.BaseURL == "") {
		return fmt.Errorf("proxy config with base_url is required for proxy mode")
	}
	if c.Mode == "isolated" && (c.Isolated == nil || c.Isolated.Runtime == "" || c.Isolated.Code == "") {
		return fmt.Errorf("isolated config with runtime and code is required for isolated mode")
	}
	return nil
}

// UpdateFunctionCommand updates an existing function
type UpdateFunctionCommand struct {
	commands.BaseCommand
	FunctionID  string                 `json:"function_id"`
	TenantID    string                 `json:"tenant_id"`
	Name        *string                `json:"name,omitempty"`
	Description *string                `json:"description,omitempty"`
	Mode        *string                `json:"mode,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Webhook     *WebhookConfig         `json:"webhook,omitempty"`
	Proxy       *ProxyConfig           `json:"proxy,omitempty"`
	Isolated    *IsolatedConfig        `json:"isolated,omitempty"`
	TimeoutMs   *int32                 `json:"timeout_ms,omitempty"`
	MemoryMB    *int32                 `json:"memory_mb,omitempty"`
	MaxRetries  *int32                 `json:"max_retries,omitempty"`
	Enabled     *bool                  `json:"enabled,omitempty"`
}

func NewUpdateFunctionCommand(functionID, tenantID, userID, traceID string) *UpdateFunctionCommand {
	return &UpdateFunctionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		FunctionID: functionID,
		TenantID:   tenantID,
	}
}

func (c UpdateFunctionCommand) AggregateID() string { return c.FunctionID }
func (c UpdateFunctionCommand) CommandType() string { return "UpdateFunction" }
func (c UpdateFunctionCommand) Validate() error {
	if c.FunctionID == "" {
		return fmt.Errorf("function_id cannot be empty")
	}
	// tenant_id is optional for self-hosted mode - will use "default" if empty
	if c.Mode != nil {
		mode := *c.Mode
		if mode != "webhook" && mode != "proxy" && mode != "isolated" {
			return fmt.Errorf("invalid mode: %s (must be webhook, proxy, or isolated)", mode)
		}
	}
	return nil
}

// DeleteFunctionCommand deletes a function
type DeleteFunctionCommand struct {
	commands.BaseCommand
	FunctionID string `json:"function_id"`
	TenantID   string `json:"tenant_id"`
}

func NewDeleteFunctionCommand(functionID, tenantID, userID, traceID string) *DeleteFunctionCommand {
	return &DeleteFunctionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		FunctionID: functionID,
		TenantID:   tenantID,
	}
}

func (c DeleteFunctionCommand) AggregateID() string { return c.FunctionID }
func (c DeleteFunctionCommand) CommandType() string { return "DeleteFunction" }
func (c DeleteFunctionCommand) Validate() error {
	if c.FunctionID == "" {
		return fmt.Errorf("function_id cannot be empty")
	}
	// tenant_id is optional for self-hosted mode - will use "default" if empty
	return nil
}
