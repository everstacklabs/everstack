package v1

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/agents/skills"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/lib/pq"
	"google.golang.org/protobuf/encoding/protojson"
)

// sseEventSender writes AgentEvent protos as SSE lines to an http.ResponseWriter.
type sseEventSender struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseEventSender) Send(msg *agentspb.AgentEvent, rawData map[string]interface{}) error {
	protoJSON, err := protojson.MarshalOptions{UseProtoNames: false, EmitUnpopulated: false}.Marshal(msg)
	if err != nil {
		logger.WithFields("event_type", msg.GetType(), "error", err.Error()).
			Error("sse_sender: failed to marshal event")
		return err
	}

	// If the runtime event carried a Data map (used by low-frequency events like
	// sandbox.template.select and sandbox.port.expose), merge it into the JSON
	// payload as a "data" key. protojson can't serialize it because AgentEvent
	// proto has no such field.
	var finalJSON []byte
	if len(rawData) > 0 {
		var merged map[string]interface{}
		if err := json.Unmarshal(protoJSON, &merged); err != nil {
			finalJSON = protoJSON // fall back to proto-only
		} else {
			merged["data"] = rawData
			if out, err := json.Marshal(merged); err == nil {
				finalJSON = out
			} else {
				finalJSON = protoJSON
			}
		}
	} else {
		finalJSON = protoJSON
	}

	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", msg.GetType(), finalJSON); err != nil {
		logger.WithFields("event_type", msg.GetType(), "error", err.Error()).
			Error("sse_sender: failed to write event")
		return err
	}
	s.flusher.Flush()
	logger.WithFields("event_type", msg.GetType(), "session_id", msg.GetSessionId()).
		Debug("sse_sender: event sent and flushed")
	return nil
}

// handleRunTurnStreamGateway handles the SSE streaming endpoint when invoked
// via grpc-gateway's HandlePath override. grpc-gateway's in-process transport
// does not support server-streaming RPCs, so this handler serves SSE directly.
func (s *Server) handleRunTurnStreamGateway(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	sessionID := pathParams["session_id"]
	logger.WithFields("session_id", sessionID).Info("handleRunTurnStreamGateway: request received")

	// Parse JSON body
	var body struct {
		TenantID        string `json:"tenant_id"`
		UserInput       string `json:"user_input"`
		EnableStreaming bool   `json:"enable_streaming"`
		EnableWebSearch bool   `json:"enable_web_search"`
		ModelOverride   string `json:"model_override,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		logger.WithFields("session_id", sessionID, "error", err.Error()).
			Error("handleRunTurnStreamGateway: failed to decode request body")
		http.Error(w, `{"error":{"code":"invalid_argument","message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}

	logger.WithFields("session_id", sessionID, "tenant_id", body.TenantID, "enable_streaming", body.EnableStreaming, "model_override", body.ModelOverride).
		Info("handleRunTurnStreamGateway: request parsed")

	// Build Connect-style request so we can reuse runTurnStreamInternal
	protoReq := &agentspb.RunTurnStreamRequest{
		TenantId:        body.TenantID,
		SessionId:       sessionID,
		UserInput:       body.UserInput,
		EnableStreaming: body.EnableStreaming,
		EnableWebSearch: body.EnableWebSearch,
	}
	connectReq := &connect.Request[agentspb.RunTurnStreamRequest]{Msg: protoReq}
	if body.ModelOverride != "" {
		connectReq.Header().Set("X-Model-Override", body.ModelOverride)
	}

	// Set up SSE response
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.WithFields("session_id", sessionID).Error("handleRunTurnStreamGateway: streaming not supported")
		http.Error(w, `{"error":{"code":"internal","message":"streaming not supported"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	logger.WithFields("session_id", sessionID).Info("handleRunTurnStreamGateway: SSE headers sent, starting stream")

	sender := &sseEventSender{w: w, flusher: flusher}

	if err := s.runTurnStreamInternal(r.Context(), connectReq, sender); err != nil {
		logger.WithFields("session_id", sessionID, "error", err.Error()).
			Error("handleRunTurnStreamGateway: runTurnStreamInternal failed")
		// If headers already sent we can only log; otherwise write error
		errData, _ := json.Marshal(map[string]interface{}{
			"error": map[string]string{
				"code":    "internal",
				"message": err.Error(),
			},
		})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
	} else {
		logger.WithFields("session_id", sessionID).Info("handleRunTurnStreamGateway: stream completed successfully")
	}
}

// handleRunCronNow handles POST /v1/agents/crons/{cron_id}/run.
func (s *Server) handleRunCronNow(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	cronIDStr := pathParams["cron_id"]
	cronID, err := strconv.ParseInt(cronIDStr, 10, 64)
	if err != nil || cronID <= 0 {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"invalid cron id"}}`, http.StatusBadRequest)
		return
	}
	if s.sandboxMgr == nil {
		http.Error(w, `{"error":{"code":"failed_precondition","message":"sandbox manager unavailable"}}`, http.StatusFailedDependency)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	if err := s.sandboxMgr.RunCronNow(ctx, cronID); err != nil {
		logger.WithFields("cron_id", cronID, "error", err.Error()).Error("handleRunCronNow: failed to run cron")
		http.Error(w, fmt.Sprintf(`{"error":{"code":"internal","message":%q}}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Cron executed",
		"cronId":  cronID,
	})
}

// handleSubscribeSessionEvents streams events from an already-running session
// as SSE. This allows UI clients that navigate away and return to catch up on
// events emitted while they were disconnected. The emitter replays all buffered
// events from the current turn, then continues streaming live events.
func (s *Server) handleSubscribeSessionEvents(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	sessionID := pathParams["session_id"]
	if sessionID == "" {
		http.Error(w, `{"error":"session_id is required"}`, http.StatusBadRequest)
		return
	}

	if s.sessionMgr == nil {
		http.Error(w, `{"error":"agent session manager not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	eventCh, doneCh, ok := s.sessionMgr.SubscribeToSession(sessionID, 128)
	if !ok {
		// Session is not running — return 204 so the frontend knows there's nothing to stream
		w.WriteHeader(http.StatusNoContent)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	sender := &sseEventSender{w: w, flusher: flusher}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, chanOk := <-eventCh:
			if !chanOk {
				return
			}
			protoEvent := runtimeEventToProto(&event)
			if err := sender.Send(protoEvent, event.Data); err != nil {
				logger.WithFields("session_id", sessionID, "error", err.Error()).
					Debug("subscribe_sse: send failed, client likely disconnected")
				return
			}
			if event.Type == agentrt.EventSessionEnd || event.Type == agentrt.EventSessionError {
				return
			}
		case <-doneCh:
			// Drain remaining events
			for {
				select {
				case event := <-eventCh:
					protoEvent := runtimeEventToProto(&event)
					_ = sender.Send(protoEvent, event.Data)
				default:
					return
				}
			}
		}
	}
}

// handleSandboxLogsSSE streams container logs as SSE events.
func (s *Server) handleSandboxLogsSSE(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	sessionID := pathParams["session_id"]
	if sessionID == "" {
		http.Error(w, `{"error":"session_id is required"}`, http.StatusBadRequest)
		return
	}

	if s.sandboxMgr == nil {
		http.Error(w, `{"error":"sandbox feature is not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	rc, err := s.sandboxMgr.Logs(r.Context(), sessionID, sandbox.LogsOptions{
		Follow:     true,
		Tail:       100,
		Timestamps: true,
	})
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
		return
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		line := scanner.Text()
		data, _ := json.Marshal(map[string]string{"line": line})
		fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		logger.WithFields("session_id", sessionID, "error", err.Error()).
			Debug("sandbox_logs_sse: scanner error")
	}
}

// handleSandboxStatsSSE streams container stats as SSE events every 2 seconds.
func (s *Server) handleSandboxStatsSSE(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	sessionID := pathParams["session_id"]
	if sessionID == "" {
		http.Error(w, `{"error":"session_id is required"}`, http.StatusBadRequest)
		return
	}

	if s.sandboxMgr == nil {
		http.Error(w, `{"error":"sandbox feature is not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	// Send an initial stats event immediately so the UI doesn't have to wait
	// for the first ticker interval before rendering data.
	if stats, err := s.sandboxMgr.Stats(r.Context(), sessionID); err == nil {
		data, _ := json.Marshal(stats)
		fmt.Fprintf(w, "event: stats\ndata: %s\n\n", data)
		flusher.Flush()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			stats, err := s.sandboxMgr.Stats(r.Context(), sessionID)
			if err != nil {
				errData, _ := json.Marshal(map[string]string{"error": err.Error()})
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
				flusher.Flush()
				return
			}

			data, _ := json.Marshal(stats)
			fmt.Fprintf(w, "event: stats\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleCreateSandbox creates a new sandbox instance via REST.
// Supports template_id: when set, template config is used as base; individual fields override.
func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if s.sandboxMgr == nil {
		http.Error(w, `{"error":"sandbox feature is not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	var body struct {
		TenantID             string   `json:"tenant_id"`
		SessionID            string   `json:"session_id"`
		Image                string   `json:"image"`
		CPULimit             float64  `json:"cpu_limit"`
		MemoryMB             float64  `json:"memory_mb"`
		DiskMB               float64  `json:"disk_mb"`
		TimeoutSeconds       float64  `json:"timeout_seconds"`
		NetworkMode          string   `json:"network_mode"`
		AllowedHosts         []string `json:"allowed_hosts"`
		IdleRetentionSeconds int      `json:"idle_retention_seconds"`
		TemplateID           string   `json:"template_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	sessionID := body.SessionID
	if sessionID == "" {
		sessionID = "manual_" + uuid.New().String()
	}
	tenantID, err := s.resolveTenantID(r.Context(), body.TenantID)
	if err != nil || tenantID == "" {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

	// Start from template config if provided, otherwise use defaults
	var cfg sandbox.SandboxConfig
	if body.TemplateID != "" {
		t := sandbox.GetTemplate(body.TemplateID)
		if t == nil {
			http.Error(w, `{"error":"unknown template: `+body.TemplateID+`"}`, http.StatusBadRequest)
			return
		}
		cfg = sandbox.TemplateToSandboxConfig(t)
	} else {
		cfg = sandbox.DefaultSandboxConfig()
		cfg.Enabled = true
	}

	// Override template defaults with any explicit request fields
	if body.Image != "" {
		cfg.Image = body.Image
	}
	if body.CPULimit > 0 && body.CPULimit <= 8 {
		cfg.CPULimit = body.CPULimit
	}
	if body.MemoryMB >= 64 && body.MemoryMB <= 8192 {
		cfg.MemoryMB = int64(body.MemoryMB)
	}
	if body.DiskMB >= 64 && body.DiskMB <= sandbox.MaxSandboxDiskMB {
		cfg.DiskMB = int64(body.DiskMB)
	}
	if body.TimeoutSeconds >= 30 && body.TimeoutSeconds <= 3600 {
		cfg.TimeoutSeconds = int(body.TimeoutSeconds)
	}
	switch body.NetworkMode {
	case "deny", "whitelist", "allow":
		cfg.NetworkMode = body.NetworkMode
	}
	if len(body.AllowedHosts) > 0 {
		cfg.AllowedHosts = body.AllowedHosts
	}
	if body.IdleRetentionSeconds != 0 {
		cfg.IdleRetentionSeconds = body.IdleRetentionSeconds
	}

	// Auto-include the port-exposure base domain so sandbox-hosted apps
	// can reach their own exposed URLs when in whitelist mode.
	s.injectPortExposureDomain(&cfg)

	createCtx := context.WithoutCancel(r.Context())
	inst, err := s.sandboxMgr.GetOrCreate(createCtx, sessionID, tenantID, cfg)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("handleCreateSandbox: failed to create sandbox")
		errResp, _ := json.Marshal(map[string]string{"error": err.Error()})
		status := http.StatusInternalServerError
		if errors.Is(err, sandbox.ErrSandboxBillingRequired) {
			status = http.StatusPaymentRequired
		} else if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			status = http.StatusTooManyRequests
		} else if errors.Is(err, sandbox.ErrUnsupportedSandboxSize) {
			status = http.StatusBadRequest
		}
		http.Error(w, string(errResp), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           inst.ID,
		"session_id":   sessionID,
		"tenant_id":    tenantID,
		"container_id": inst.ContainerID,
		"status":       string(inst.Status),
		"backend":      inst.Backend,
		"image":        inst.Config.Image,
		"created_at":   inst.CreatedAt.Format(time.RFC3339),
		"expires_at":   inst.ExpiresAt.Format(time.RFC3339),
	})
}

// handleCreateSandboxConnect handles CreateSandbox via ConnectRPC-style JSON.
// Request body uses camelCase keys (ConnectRPC JSON convention).
// Supports template_id: when set, template config is used as base; individual fields override.
func (s *Server) handleCreateSandboxConnect(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"code": "unavailable", "message": "sandbox feature is not enabled"})
		return
	}

	var body struct {
		TenantID             string   `json:"tenantId"`
		SessionID            string   `json:"sessionId"`
		Image                string   `json:"image"`
		CpuLimit             float64  `json:"cpuLimit"`
		MemoryMb             float64  `json:"memoryMb"`
		DiskMb               float64  `json:"diskMb"`
		TimeoutSeconds       float64  `json:"timeoutSeconds"`
		NetworkMode          string   `json:"networkMode"`
		AllowedHosts         []string `json:"allowedHosts"`
		IdleRetentionSeconds int      `json:"idleRetentionSeconds"`
		TemplateID           string   `json:"templateId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"code": "invalid_argument", "message": "invalid JSON body"})
		return
	}

	sessionID := body.SessionID
	if sessionID == "" {
		sessionID = "manual_" + uuid.New().String()
	}
	tenantID, err := s.resolveTenantID(r.Context(), body.TenantID)
	if err != nil || tenantID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"code": "unauthenticated", "message": "authentication required"})
		return
	}

	// Start from template config if provided, otherwise use defaults
	var cfg sandbox.SandboxConfig
	if body.TemplateID != "" {
		t := sandbox.GetTemplate(body.TemplateID)
		if t == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"code": "invalid_argument", "message": "unknown template: " + body.TemplateID})
			return
		}
		cfg = sandbox.TemplateToSandboxConfig(t)
	} else {
		cfg = sandbox.DefaultSandboxConfig()
		cfg.Enabled = true
	}

	// Override template defaults with any explicit request fields
	if body.Image != "" {
		cfg.Image = body.Image
	}
	if body.CpuLimit > 0 && body.CpuLimit <= 8 {
		cfg.CPULimit = body.CpuLimit
	}
	if body.MemoryMb >= 64 && body.MemoryMb <= 8192 {
		cfg.MemoryMB = int64(body.MemoryMb)
	}
	if body.DiskMb >= 64 && body.DiskMb <= sandbox.MaxSandboxDiskMB {
		cfg.DiskMB = int64(body.DiskMb)
	}
	if body.TimeoutSeconds >= 30 && body.TimeoutSeconds <= 3600 {
		cfg.TimeoutSeconds = int(body.TimeoutSeconds)
	}
	switch body.NetworkMode {
	case "deny", "whitelist", "allow":
		cfg.NetworkMode = body.NetworkMode
	}
	if len(body.AllowedHosts) > 0 {
		cfg.AllowedHosts = body.AllowedHosts
	}
	if body.IdleRetentionSeconds != 0 {
		cfg.IdleRetentionSeconds = body.IdleRetentionSeconds
	}

	s.injectPortExposureDomain(&cfg)

	// Use a detached context so container creation completes even if the
	// HTTP client disconnects (e.g. browser navigates away).
	createCtx := context.WithoutCancel(r.Context())
	inst, err := s.sandboxMgr.GetOrCreate(createCtx, sessionID, tenantID, cfg)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("handleCreateSandboxConnect: failed to create sandbox")
		w.Header().Set("Content-Type", "application/json")
		status, code := http.StatusInternalServerError, "internal"
		if errors.Is(err, sandbox.ErrSandboxBillingRequired) {
			status, code = http.StatusPaymentRequired, "failed_precondition"
		} else if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			status, code = http.StatusTooManyRequests, "resource_exhausted"
		} else if errors.Is(err, sandbox.ErrUnsupportedSandboxSize) {
			status, code = http.StatusBadRequest, "invalid_argument"
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"code": code, "message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          inst.ID,
		"sessionId":   sessionID,
		"tenantId":    tenantID,
		"containerId": inst.ContainerID,
		"status":      string(inst.Status),
		"backend":     inst.Backend,
		"image":       inst.Config.Image,
		"createdAt":   inst.CreatedAt.Format(time.RFC3339),
		"expiresAt":   inst.ExpiresAt.Format(time.RFC3339),
	})
}

// HandleSandboxLogsHTTP wraps handleSandboxLogsSSE for direct HTTP handler registration
// (bypassing the /v1 grpc-gateway middleware chain that requires API keys).
func (s *Server) HandleSandboxLogsHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxLogsSSE(w, r, mux.Vars(r))
}

// HandleSandboxStatsHTTP wraps handleSandboxStatsSSE for direct HTTP handler registration
// (bypassing the /v1 grpc-gateway middleware chain that requires API keys).
func (s *Server) HandleSandboxStatsHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxStatsSSE(w, r, mux.Vars(r))
}

// handleMemorySetupGateway handles the memory setup endpoint via grpc-gateway.
func (s *Server) handleMemorySetupGateway(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	s.handleSetupMemoryConnect(w, r)
}

// handleRecreateSandboxConnect recreates a sandbox from an expired instance's config.
func (s *Server) handleRecreateSandboxConnect(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"code": "unavailable", "message": "sandbox feature is not enabled"})
		return
	}

	var body struct {
		TenantID  string `json:"tenantId"`
		SandboxID string `json:"sandboxId"`
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"code": "invalid_argument", "message": "invalid JSON body"})
		return
	}

	if body.SandboxID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"code": "invalid_argument", "message": "sandboxId is required"})
		return
	}

	// The authenticated context is the only trusted billing identity. Never
	// accept a tenant ID from the body: that would let an unpaid tenant charge
	// compute to another organization's Stripe subscription.
	tenantID, err := s.resolveTenantID(r.Context(), body.TenantID)
	if err != nil || tenantID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"code": "unauthenticated", "message": "authentication required"})
		return
	}
	if !s.sandboxOwnedByTenant(r.Context(), body.SandboxID, tenantID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": "sandbox not found"})
		return
	}

	// Load the expired instance's config from DB
	cfg, _, err := s.sandboxMgr.GetInstanceConfig(r.Context(), body.SandboxID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": err.Error()})
		return
	}

	// Generate a new session ID if not provided
	sessionID := body.SessionID
	if sessionID == "" {
		sessionID = "manual_" + uuid.New().String()
	}

	// Build sandbox config from the stored instance config
	sandboxCfg := sandbox.SandboxConfig{
		Enabled:        true,
		Image:          cfg.Image,
		CPULimit:       cfg.CPULimit,
		MemoryMB:       cfg.MemoryMB,
		DiskMB:         cfg.DiskMB,
		TimeoutSeconds: cfg.TimeoutSeconds,
		NetworkMode:    string(cfg.NetworkMode),
		AllowedHosts:   cfg.AllowedHosts,
		EnvVars:        cfg.EnvVars,
	}

	recreateCtx := context.WithoutCancel(r.Context())
	inst, err := s.sandboxMgr.GetOrCreate(recreateCtx, sessionID, tenantID, sandboxCfg)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("handleRecreateSandbox: failed to recreate sandbox")
		w.Header().Set("Content-Type", "application/json")
		status, code := http.StatusInternalServerError, "internal"
		if errors.Is(err, sandbox.ErrSandboxBillingRequired) {
			status, code = http.StatusPaymentRequired, "failed_precondition"
		} else if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			status, code = http.StatusTooManyRequests, "resource_exhausted"
		} else if errors.Is(err, sandbox.ErrUnsupportedSandboxSize) {
			status, code = http.StatusBadRequest, "invalid_argument"
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"code": code, "message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          inst.ID,
		"sessionId":   sessionID,
		"tenantId":    tenantID,
		"containerId": inst.ContainerID,
		"status":      string(inst.Status),
		"backend":     inst.Backend,
		"image":       inst.Config.Image,
		"createdAt":   inst.CreatedAt.Format(time.RFC3339),
		"expiresAt":   inst.ExpiresAt.Format(time.RFC3339),
	})
}

// handleListSandboxTemplatesConnect returns the code-defined template catalog.
func (s *Server) handleListSandboxTemplatesConnect(w http.ResponseWriter, _ *http.Request) {
	templates := sandbox.ListTemplates()
	out := make([]map[string]interface{}, 0, len(templates))
	for _, t := range templates {
		out = append(out, templateToJSON(t))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": out,
	})
}

// handleGetSandboxTemplateConnect returns a single template by ID or slug.
func (s *Server) handleGetSandboxTemplateConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TemplateID string `json:"templateId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"code": "invalid_argument", "message": "invalid JSON body"})
		return
	}

	t := sandbox.GetTemplate(body.TemplateID)
	if t == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": "template not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"template": templateToJSON(*t),
	})
}

// handleListSandboxTemplatesGateway handles the REST GET /v1/sandbox/templates endpoint.
func (s *Server) handleListSandboxTemplatesGateway(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	templates := sandbox.ListTemplates()
	out := make([]map[string]interface{}, 0, len(templates))
	for _, t := range templates {
		out = append(out, templateToJSON(t))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": out,
	})
}

// handleGetSandboxTemplateGateway handles the REST GET /v1/sandbox/templates/{template_id} endpoint.
func (s *Server) handleGetSandboxTemplateGateway(w http.ResponseWriter, _ *http.Request, pathParams map[string]string) {
	templateID := pathParams["template_id"]
	t := sandbox.GetTemplate(templateID)
	if t == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "template not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"template": templateToJSON(*t),
	})
}

func templateToJSON(t sandbox.Template) map[string]interface{} {
	m := map[string]interface{}{
		"id":             t.ID,
		"name":           t.Name,
		"slug":           t.Slug,
		"description":    t.Description,
		"icon":           t.Icon,
		"iconColor":      t.IconColor,
		"image":          t.Image,
		"cpuLimit":       t.CPULimit,
		"memoryMb":       t.MemoryMB,
		"diskMb":         t.DiskMB,
		"timeoutSeconds": t.TimeoutSecs,
		"networkMode":    t.NetworkMode,
		"workDir":        t.WorkDir,
	}
	if len(t.EnvVars) > 0 {
		m["envVars"] = t.EnvVars
	}
	if len(t.Tags) > 0 {
		m["tags"] = t.Tags
	}
	return m
}

// handleSandboxEventsSSE streams sandbox lifecycle events as SSE.
// Queries initial events from DB, then polls for new events every 2s.
func (s *Server) handleSandboxEventsSSE(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	sandboxID := pathParams["sandbox_id"]
	if sandboxID == "" {
		http.Error(w, `{"error":"sandbox_id is required"}`, http.StatusBadRequest)
		return
	}

	if s.sandboxMgr == nil {
		http.Error(w, `{"error":"sandbox feature is not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	db := s.sandboxMgr.DB()
	if db == nil {
		errData, _ := json.Marshal(map[string]string{"error": "database not available"})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
		return
	}

	var lastID int64

	// Send initial batch
	var events []sandbox.SandboxEvent
	const initialQ = `SELECT * FROM sandbox_events WHERE sandbox_id = $1 ORDER BY created_at ASC LIMIT 200`
	if err := db.SelectContext(r.Context(), &events, initialQ, sandboxID); err == nil {
		for _, e := range events {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: sandbox_event\ndata: %s\n\n", data)
			if e.ID > lastID {
				lastID = e.ID
			}
		}
		flusher.Flush()
	}

	// Poll for new events every 2s
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			var newEvents []sandbox.SandboxEvent
			const pollQ = `SELECT * FROM sandbox_events WHERE sandbox_id = $1 AND id > $2 ORDER BY created_at ASC LIMIT 50`
			if err := db.SelectContext(r.Context(), &newEvents, pollQ, sandboxID, lastID); err != nil {
				continue
			}
			for _, e := range newEvents {
				data, _ := json.Marshal(e)
				fmt.Fprintf(w, "event: sandbox_event\ndata: %s\n\n", data)
				if e.ID > lastID {
					lastID = e.ID
				}
			}
			if len(newEvents) > 0 {
				flusher.Flush()
			}
		}
	}
}

// HandleSandboxEventsHTTP wraps handleSandboxEventsSSE for direct HTTP handler registration.
func (s *Server) HandleSandboxEventsHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxEventsSSE(w, r, mux.Vars(r))
}

// handleSetupMemoryConnect handles SetupMemory via ConnectRPC-style JSON.
// It idempotently sets up the pgvector extension and wires the memory backend.
func (s *Server) handleSetupMemoryConnect(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"code": "unavailable", "message": "database not available"})
		return
	}

	if err := memory.EnsurePgVector(r.Context(), s.db); err != nil {
		logger.WithFields("error", err.Error()).Error("handleSetupMemory: pgvector setup failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"code":    "internal",
			"message": err.Error(),
		})
		return
	}

	// Create the store and wire it up if not already done
	if s.memoryStore == nil {
		pgStore, err := memory.NewPgVectorStore(s.db)
		if err != nil {
			logger.WithFields("error", err.Error()).Error("handleSetupMemory: store creation failed")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"code":    "internal",
				"message": err.Error(),
			})
			return
		}
		memStore := memory.NewTracedVectorStore(pgStore)

		// Create embedder using the request's tenant-scoped registry/router.
		if s.engine != nil {
			registry, router, providerErr := s.engine.ProvidersForContext(r.Context())
			if providerErr != nil {
				logger.WithFields("error", providerErr.Error()).Error("handleSetupMemory: provider resolution failed")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"code":    "internal",
					"message": providerErr.Error(),
				})
				return
			}
			memEmbedder := memory.NewTracedEmbedder(memory.NewEmbedder(registry, router))
			s.SetMemoryBackend(memStore, memEmbedder)
		} else {
			s.memoryStore = memStore
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "pgvector memory backend initialized",
		"backend": "pgvector",
	})
}

// HandleSubmitUserInputHTTP wraps handleSubmitUserInput for direct HTTP handler registration
// (bypassing the /v1 grpc-gateway middleware chain that requires API keys).
func (s *Server) HandleSubmitUserInputHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleSubmitUserInput(w, r, mux.Vars(r))
}

// HandleStopSessionTurnHTTP wraps handleStopSessionTurn for direct HTTP handler registration.
func (s *Server) HandleStopSessionTurnHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleStopSessionTurn(w, r, mux.Vars(r))
}

// handleSubmitUserInput handles POST /v1/agents/sessions/{session_id}/user-input
// to deliver a user's response to a pending ask_user tool call.
func (s *Server) handleSubmitUserInput(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	if s.sessionMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "agent session manager not initialized"})
		return
	}

	var body struct {
		InputID string `json:"input_id"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON body"})
		return
	}

	if body.InputID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "input_id is required"})
		return
	}

	if err := s.sessionMgr.SubmitUserInput(r.Context(), body.InputID, body.Text); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"input_id": body.InputID,
	})
}

// handleListSandboxFiles lists directory contents inside a sandbox for file-mention support.
// GET /v1/sandbox/{session_id}/files?path=/repo
func (s *Server) handleListSandboxFiles(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	if s.sandboxMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "unavailable", "message": "sandbox feature is not enabled"},
		})
		return
	}

	sessionID := pathParams["session_id"]
	if sessionID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "invalid_argument", "message": "session_id is required"},
		})
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	// Path validation
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, "/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "invalid_argument", "message": "path must be absolute"},
		})
		return
	}

	// Enforce root allowlist: only /repo, /workspace, or subpaths thereof
	allowedRoots := []string{"/repo", "/workspace"}
	allowed := false
	for _, root := range allowedRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			allowed = true
			break
		}
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "invalid_argument", "message": "path must be under /repo or /workspace"},
		})
		return
	}

	// Note: This is a pre-registered route that bypasses gRPC-gateway middleware,
	// so CQRS context is not available. We rely on sandboxMgr.ListFiles to validate
	// the session (returns "no sandbox for session" if invalid), matching the pattern
	// used by handleSandboxLogsSSE and handleSandboxStatsSSE.
	files, err := s.sandboxMgr.ListFiles(r.Context(), sessionID, path)
	if err != nil {
		if strings.Contains(err.Error(), "no sandbox for session") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{"code": "not_found", "message": err.Error()},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "internal", "message": err.Error()},
		})
		return
	}

	// Map to camelCase response
	filesJSON := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		filesJSON = append(filesJSON, map[string]interface{}{
			"name":  f.Name,
			"path":  f.Path,
			"size":  f.Size,
			"isDir": f.IsDir,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":  path,
		"files": filesJSON,
	})
}

// HandleSandboxFilesHTTP wraps handleListSandboxFiles for direct HTTP handler registration
// (bypassing the /v1 grpc-gateway middleware chain that requires API keys).
func (s *Server) HandleSandboxFilesHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleListSandboxFiles(w, r, mux.Vars(r))
}

// handleSearchSandboxFiles recursively searches for files matching a query inside a sandbox.
// GET /v1/sandbox/{session_id}/files/search?query=foo&path=/repo
func (s *Server) handleSearchSandboxFiles(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	if s.sandboxMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "unavailable", "message": "sandbox feature is not enabled"},
		})
		return
	}

	sessionID := pathParams["session_id"]
	if sessionID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "invalid_argument", "message": "session_id is required"},
		})
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "invalid_argument", "message": "query parameter is required"},
		})
		return
	}

	rootPath := r.URL.Query().Get("path")
	if rootPath == "" {
		rootPath = "/repo"
	}

	files, err := s.sandboxMgr.SearchFiles(r.Context(), sessionID, rootPath, query, 50)
	if err != nil {
		if strings.Contains(err.Error(), "no sandbox for session") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{"code": "not_found", "message": err.Error()},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "internal", "message": err.Error()},
		})
		return
	}

	filesJSON := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		filesJSON = append(filesJSON, map[string]interface{}{
			"name":  f.Name,
			"path":  f.Path,
			"size":  f.Size,
			"isDir": f.IsDir,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"files": filesJSON,
	})
}

// HandleSearchSandboxFilesHTTP wraps handleSearchSandboxFiles for direct HTTP handler registration.
func (s *Server) HandleSearchSandboxFilesHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleSearchSandboxFiles(w, r, mux.Vars(r))
}

// handleStopSessionTurn handles POST /v1/agents/sessions/{session_id}/turns/stop.
// It interrupts the active turn without cancelling/completing the session.
func (s *Server) handleStopSessionTurn(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	if s.sessionMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "agent session manager not initialized"})
		return
	}

	sessionID := pathParams["session_id"]
	if sessionID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "session_id is required"})
		return
	}

	var body struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.TenantID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "tenant_id is required"})
		return
	}

	sys, err := cqrs.GetSystemFromContext(r.Context())
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "CQRS system not available"})
		return
	}

	// Ensure the session exists for the caller's tenant before interrupting.
	sessionQ := agentsquery.NewGetSessionByIDQuery(sessionID, body.TenantID)
	sessionRes, err := sys.QueryBus.Execute(r.Context(), sessionQ)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if sessionRes == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "session not found"})
		return
	}

	if err := s.sessionMgr.InterruptSession(sessionID); err != nil {
		// Idempotent/no-op when nothing is actively running.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "no active turn to stop",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "active turn interrupted",
	})
}

// handleAgentCapabilities returns feature availability flags for the agent UI.
// GET /v1/agents/capabilities
func (s *Server) handleAgentCapabilities(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	webSearchAvailable := os.Getenv("EVS_SEARXNG_URL") != ""
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"web_search_available": webSearchAvailable,
	})
}

// HandleAgentCapabilitiesHTTP wraps handleAgentCapabilities for direct HTTP handler registration
// (bypassing the /v1 grpc-gateway middleware chain that requires API keys).
func (s *Server) HandleAgentCapabilitiesHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleAgentCapabilities(w, r, nil)
}

// handleResolveSkill fetches and parses SKILL.md files from a GitHub repo spec.
// POST /v1/agents/skills/resolve
func (s *Server) handleResolveSkill(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	var body struct {
		Spec string `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Spec == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "spec is required"})
		return
	}

	spec, err := skills.ParseSkillSpec(body.Spec)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	defs, err := skills.FetchSkills(r.Context(), *spec)
	if err != nil {
		logger.WithFields("spec", body.Spec, "error", err.Error()).Warn("handleResolveSkill: fetch failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"skills": defs,
	})
}

// handleRegistryBrowse proxies browse requests to skills.sh leaderboard API.
// GET /v1/agents/skills/registry/browse?view=all-time&page=0
func (s *Server) handleRegistryBrowse(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "all-time"
	}
	pageStr := r.URL.Query().Get("page")
	page := 0
	if pageStr != "" {
		if _, err := fmt.Sscanf(pageStr, "%d", &page); err != nil {
			page = 0
		}
	}

	result, err := skills.RegistryBrowse(r.Context(), view, page)
	if err != nil {
		logger.WithFields("view", view, "page", page, "error", err.Error()).Warn("handleRegistryBrowse: failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleRegistrySearch proxies search requests to skills.sh search API.
// GET /v1/agents/skills/registry/search?q=frontend&limit=50
func (s *Server) handleRegistrySearch(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	query := r.URL.Query().Get("q")
	if len(query) < 2 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "query must be at least 2 characters"})
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if _, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil {
			limit = 50
		}
	}

	result, err := skills.RegistrySearch(r.Context(), query, limit)
	if err != nil {
		logger.WithFields("query", query, "error", err.Error()).Warn("handleRegistrySearch: failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ============================================================================
// Gateway & Egress REST Handlers
// ============================================================================

func (s *Server) handleGetGatewayStatus(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	// Count active port mappings from DB
	var activeRoutes int
	if s.db != nil {
		_ = s.db.GetContext(r.Context(), &activeRoutes,
			`SELECT COUNT(*) FROM sandbox_port_mappings WHERE status = 'active'`)
	}

	resp := map[string]interface{}{
		"listener_addr":           s.portExposureListenPort,
		"base_domain":             s.portExposureBaseDomain,
		"tls_enabled":             s.portExposureTLSEnabled,
		"active_routes":           activeRoutes,
		"session_routing_enabled": false,
		"healthy":                 s.sandboxMgr != nil,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleListEgressEvents(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	tenantID, err := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid tenant"})
		return
	}

	sandboxID := r.URL.Query().Get("sandbox_id")
	action := r.URL.Query().Get("action")
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}

	if s.db == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"events": []interface{}{}, "total_count": 0})
		return
	}

	// Build event type filter from action
	var eventTypes []string
	switch action {
	case "allowed":
		eventTypes = []string{"egress.allowed"}
	case "blocked":
		eventTypes = []string{"egress.blocked"}
	default:
		eventTypes = []string{"egress.allowed", "egress.blocked"}
	}

	q := `SELECT id, sandbox_id, session_id, tenant_id, event_type, message, metadata, created_at
		FROM sandbox_events
		WHERE tenant_id = $1 AND sandbox_id = $2 AND event_type = ANY($3)
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`

	type eventRow struct {
		ID        string    `db:"id" json:"id"`
		SandboxID string    `db:"sandbox_id" json:"sandbox_id"`
		SessionID string    `db:"session_id" json:"session_id"`
		TenantID  string    `db:"tenant_id" json:"tenant_id"`
		EventType string    `db:"event_type" json:"event_type"`
		Message   string    `db:"message" json:"message"`
		Metadata  []byte    `db:"metadata" json:"-"`
		CreatedAt time.Time `db:"created_at" json:"created_at"`
	}

	var rows []eventRow
	if err := s.db.SelectContext(r.Context(), &rows, q, tenantID, sandboxID, pq.Array(eventTypes), limit, offset); err != nil {
		logger.WithFields("error", err.Error()).Warn("handleListEgressEvents: query failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "query failed"})
		return
	}

	// Count total
	var total int
	countQ := `SELECT COUNT(*) FROM sandbox_events WHERE tenant_id = $1 AND sandbox_id = $2 AND event_type = ANY($3)`
	_ = s.db.GetContext(r.Context(), &total, countQ, tenantID, sandboxID, pq.Array(eventTypes))

	type egressEvent struct {
		ID        string `json:"id"`
		SandboxID string `json:"sandbox_id"`
		Domain    string `json:"domain"`
		Action    string `json:"action"`
		QueryType string `json:"query_type"`
		CreatedAt string `json:"created_at"`
	}

	events := make([]egressEvent, 0, len(rows))
	for _, row := range rows {
		actionStr := "allowed"
		if row.EventType == "egress.blocked" {
			actionStr = "blocked"
		}

		domain := row.Message
		queryType := "A"
		if len(row.Metadata) > 0 {
			var meta map[string]interface{}
			if err := json.Unmarshal(row.Metadata, &meta); err == nil {
				if d, ok := meta["domain"].(string); ok {
					domain = d
				}
				if qt, ok := meta["query_type"].(string); ok {
					queryType = qt
				}
			}
		}

		events = append(events, egressEvent{
			ID:        fmt.Sprintf("%v", row.ID),
			SandboxID: row.SandboxID,
			Domain:    domain,
			Action:    actionStr,
			QueryType: queryType,
			CreatedAt: row.CreatedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"events":      events,
		"total_count": total,
	})
}

func (s *Server) handleGetEgressPolicy(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	sandboxID := r.URL.Query().Get("sandbox_id")

	mode := "allow"
	var allowedHosts []string

	if s.sandboxMgr != nil && sandboxID != "" {
		if inst, ok := s.sandboxMgr.GetInstance(sandboxID); ok && inst != nil {
			mode = string(inst.Config.NetworkMode)
			allowedHosts = inst.Config.AllowedHosts
		}
	}

	if allowedHosts == nil {
		allowedHosts = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"mode":          mode,
		"allowed_hosts": allowedHosts,
	})
}

// handleSubscribeAgentLifecycle streams per-agent lifecycle transitions
// (provisioning, idle, recovery_pending, …) over SSE so the UI can render
// status badges live instead of polling agent_definitions.lifecycle_status.
// The subscription is tenant-scoped: we verify the agent belongs to the
// authenticated tenant before opening the stream. Subscribers receive
// every event published on agents:lifecycle:{agent_id} until they
// disconnect or the request context is cancelled.
func (s *Server) handleSubscribeAgentLifecycle(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	agentID := pathParams["agent_id"]
	if agentID == "" {
		http.Error(w, `{"error":"agent_id is required"}`, http.StatusBadRequest)
		return
	}
	if s.lifecycleBus == nil {
		http.Error(w, `{"error":"lifecycle bus not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	tenantID, err := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	if err != nil || tenantID == "" {
		http.Error(w, `{"error":"invalid tenant"}`, http.StatusBadRequest)
		return
	}

	// Verify the agent belongs to this tenant. Without this, anyone with a
	// valid session could subscribe to another tenant's agent lifecycle
	// channel — Redis Pub/Sub does not enforce isolation.
	if s.db != nil {
		var owned bool
		checkCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := s.db.GetContext(checkCtx, &owned,
			`SELECT EXISTS(SELECT 1 FROM agent_definitions
			               WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
			agentID, tenantID)
		cancel()
		if err != nil {
			logger.WithFields("agent_id", agentID, "error", err.Error()).
				Warn("subscribe_lifecycle: ownership check failed")
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
			return
		}
		if !owned {
			http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	subCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	eventCh, err := s.lifecycleBus.Subscribe(subCtx, agentID, tenantID)
	if err != nil {
		logger.WithFields("agent_id", agentID, "error", err.Error()).
			Warn("subscribe_lifecycle: subscribe failed")
		http.Error(w, `{"error":"subscribe failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	// Send an initial "ready" event so the client knows the stream is open
	// and any proxies between the gateway and the browser flush the headers.
	fmt.Fprintf(w, "event: ready\ndata: {\"agent_id\":%q}\n\n", agentID)
	flusher.Flush()

	// Heartbeat every 25s — long enough to be cheap, short enough that
	// most idle-connection timeouts (load balancers, browsers) don't trip.
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-subCtx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case evt, ok := <-eventCh:
			if !ok {
				return
			}
			// Skip events that don't belong to the authenticated tenant.
			// publishLifecycle always sets TenantID, but be defensive — a
			// channel collision (unlikely with UUIDs) could otherwise leak.
			if evt.TenantID != "" && evt.TenantID != tenantID {
				continue
			}
			payload, marshalErr := json.Marshal(evt)
			if marshalErr != nil {
				logger.WithFields("agent_id", agentID, "error", marshalErr.Error()).
					Warn("subscribe_lifecycle: marshal failed")
				continue
			}
			if _, err := fmt.Fprintf(w, "event: lifecycle\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
