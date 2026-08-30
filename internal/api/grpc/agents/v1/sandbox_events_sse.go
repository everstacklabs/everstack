package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// HandleSandboxEvents serves Server-Sent Events for sandbox lifecycle
// state changes within a tenant. The FE opens one EventSource per
// admin sandboxes page mount and replaces its 1.5–5s polling loop
// with push updates that arrive within ~50ms of the DB write.
//
// Endpoint: GET /v1/sandboxes/events
// Tenant   : resolved from request ctx (tenant_middleware sets it
//
//	from the authenticated cookie). No tenantId query
//	param — that would let any caller subscribe to any
//	tenant's stream.
func (s *Server) HandleSandboxEvents(w http.ResponseWriter, r *http.Request) {
	// Reconciler-flag gate. Without the reconciler, no NOTIFY trigger
	// is firing, so this endpoint would just hang. Return 503 fast so
	// the FE knows to fall back to polling.
	if s.eventBus == nil {
		http.Error(w, `{"error":"sandbox events stream not configured"}`,
			http.StatusServiceUnavailable)
		return
	}

	tenantID := contextkeys.GetTenantID(r.Context())
	if tenantID == "" {
		// Try the legacy ctx extractor as a fallback.
		tenantID = contextkeys.ExtractTenantID(r.Context())
	}
	if tenantID == "" {
		// Last-ditch fallback: pull from the resolved TenantConfig the
		// middleware stashed on the request. Covers the case where a
		// tenant_config row has an empty organization_id — auth still
		// passes via OrgSlug, but contextkeys.WithTenantID stored "".
		if tc := tenant.ConfigFromContext(r.Context()); tc != nil {
			switch {
			case tc.OrganizationID != "":
				tenantID = tc.OrganizationID
			case tc.InstanceID != "":
				tenantID = tc.InstanceID
			}
			if tenantID == "" {
				logger.WithFields(
					"instance_id", tc.InstanceID,
					"slug", tc.Slug,
					"org_slug", tc.OrgSlug,
				).Error("sandbox_events_sse: tenant config has no usable identifier")
			}
		}
	}
	if tenantID == "" {
		// 503 (not 400) so the FE's single-shot probe falls back to
		// polling silently instead of surfacing a console error.
		http.Error(w, `{"error":"tenant resolution failed"}`,
			http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported by this server"}`,
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx response buffering
	// Defensive CORS for the SSE response. The tenant CORS middleware
	// (NewRuntimeCORS, applied in internal/api/api.go) owns the real policy,
	// but SSE responses can hit CDN / proxy edge cases where it isn't applied.
	//
	// Only the request's own origin is echoed. This used to reflect whatever
	// Origin arrived alongside Access-Control-Allow-Credentials: true, which
	// let any page on the internet read a logged-in user's sandbox event
	// stream cross-origin. Unlike request headers used for authentication,
	// Origin is set honestly by the browser, so an allow-list decision here is
	// meaningful: the attacker's page cannot claim to be the gateway's origin.
	w.Header().Add("Vary", "Origin")
	if origin := r.Header.Get("Origin"); origin != "" && originMatchesHost(origin, r) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	subID, ch := s.eventBus.Subscribe(tenantID)
	defer s.eventBus.Unsubscribe(tenantID, subID)

	logger.WithFields(
		"tenant_id", tenantID,
		"sub_id", subID,
		"remote", r.RemoteAddr,
	).Debug("sandbox_events_sse: subscriber connected")

	// Wait briefly for the EventBus to confirm its LISTEN connection
	// is live. Subscribe returns a channel synchronously, but the
	// actual PG NOTIFY pipe may still be connecting on cold starts.
	// Sending 'ready' before the bus is up would lie to the client.
	// Bound the wait at 3s so a wedged listener doesn't hang the
	// upgrade — we degrade to 'pending' and let the FE decide whether
	// to fall back to polling.
	if !s.eventBus.Ready() {
		readyCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		waited := waitForBusReady(readyCtx, s.eventBus)
		cancel()
		if !waited {
			if err := writeSSEFrame(w, "pending", []byte(`{"ready":false,"reason":"listener_not_connected"}`)); err != nil {
				return
			}
			flusher.Flush()
			// Continue — events that arrive after reconnect still flow.
		}
	}
	if s.eventBus.Ready() {
		if err := writeSSEFrame(w, "ready", []byte(`{"ready":true}`)); err != nil {
			return
		}
		flusher.Flush()
	}

	// Heartbeat keeps the connection alive through reverse proxies
	// that close idle connections. Standard SSE comment frames per
	// RFC 8895; clients ignore them.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case evt, alive := <-ch:
			if !alive {
				// EventBus closed the channel (e.g. shutdown).
				return
			}
			evt.LifecycleState = sandbox.PublicLifecycleState(evt.LifecycleState, sandbox.Status(evt.Status))
			evt.Status = publicSandboxEventStatus(evt.Status)
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			if err := writeSSEFrame(w, "lifecycle", payload); err != nil {
				return
			}
			flusher.Flush()

		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func publicSandboxEventStatus(status string) string {
	switch status {
	case "pending", "creating", "provisioning", "stopping", "reviving", "restoring":
		return "pending"
	case "running":
		return "running"
	case "stopped", "sleeping", "archiving", "archived", "terminating", "terminated", "deleting", "deleted":
		return "stopped"
	case "failed", "error":
		return "failed"
	default:
		return status
	}
}

// waitForBusReady polls bus.Ready() until it returns true or ctx
// expires. Cheap busy-wait with 50ms sleep — the bus typically
// connects in <1s on a cold start.
func waitForBusReady(ctx context.Context, bus *sandboxlc.EventBus) bool {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		if bus.Ready() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

// writeSSEFrame writes a single named SSE event with a JSON data
// payload. event is the event-type label (FE selects via
// EventSource.addEventListener).
func writeSSEFrame(w http.ResponseWriter, event string, data []byte) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}

// SetEventBus wires the sandbox-events EventBus into the server so the
// SSE handler can subscribe. Pass nil to disable the endpoint (which
// then returns 503).
func (s *Server) SetEventBus(bus *sandboxlc.EventBus) {
	s.eventBus = bus
}

// _ keeps the context import used even when handler stubs change.
var _ = context.Background

// originMatchesHost reports whether the Origin header names the same host the
// request was sent to. Used for the SSE defensive CORS headers so an arbitrary
// third-party origin is never echoed back with credentials allowed.
func originMatchesHost(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
