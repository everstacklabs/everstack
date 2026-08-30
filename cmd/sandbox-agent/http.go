package main

// HTTP control plane for the in-guest agent. Listens on :8080 and is the
// canonical liveness/command channel from the host. Modeled after e2b's
// envd, where a host-side health loop polls a tiny HTTP endpoint over
// the TAP IP at 20s × 100ms — completely independent of vsock, which
// keeps the "vsock wedged" failure class from masquerading as agent
// death.
//
// Surface area: GET /health, WebSocket /shell, /lsp/*, /computer/*, and
// /toolbox/* JSON endpoints for exec/files/mounts. Host callers can migrate
// module by module from legacy vsock to this listener.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

const maxHTTPToolboxBody = 16 << 20

// startHTTPServer brings up the HTTP control plane on httpListenPort.
// Returns the *http.Server so main() can Shutdown() it cleanly on
// SIGTERM. Failures to bind (port in use, kernel out of FDs) are logged
// and the function returns nil — the vsock control plane keeps working,
// the host's health probe will just fail and surface the issue.
func startHTTPServer(ctx context.Context) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	// /shell — WebSocket-upgraded interactive shell. Phase 5a ships
	// the endpoint; fcagent flips its dialer over to it in Phase 5b.
	// See cmd/sandbox-agent/shell_ws.go for the protocol contract.
	mux.HandleFunc("/shell", handleShellWS)
	// /lsp/{lang}/{op} — Language Server Protocol endpoints.
	// Starts a language server (pylsp, typescript-language-server) on demand
	// and proxies LSP JSON-RPC requests over HTTP.
	// See cmd/sandbox-agent/lsp.go for the full implementation.
	mux.HandleFunc("/lsp/", handleLSP)

	// /computer/* — Computer Use endpoints (screenshot, mouse/keyboard, recordings).
	// Requires SANDBOX_COMPUTER_USE=1. See computer_use.go.
	mux.HandleFunc("/computer/", handleComputerUse)

	// /toolbox/* — HTTP toolbox surface using the same request/response wire
	// types as the legacy vsock JSON-RPC path. Host callers can migrate module by
	// module without changing guest-side behavior.
	mux.HandleFunc("/toolbox/exec", handleHTTPToolboxRPC(toolbox.MethodExec))
	mux.HandleFunc("/toolbox/files/write", handleHTTPToolboxRPC(toolbox.MethodWriteFile))
	mux.HandleFunc("/toolbox/files/read", handleHTTPToolboxRPC(toolbox.MethodReadFile))
	mux.HandleFunc("/toolbox/files/list", handleHTTPToolboxRPC(toolbox.MethodListFiles))
	mux.HandleFunc("/toolbox/sessions/list", handleHTTPToolboxRPC(toolbox.MethodSessionList))
	mux.HandleFunc("/toolbox/sessions/kill", handleHTTPToolboxRPC(toolbox.MethodSessionKill))
	mux.HandleFunc("/toolbox/mounts/configure", handleHTTPToolboxRPC(toolbox.MethodConfigureMounts))
	srv := &http.Server{
		Addr: fmt.Sprintf(":%d", httpListenPort),
		// authMiddleware enforces the per-VM bearer token on every endpoint
		// except /health. Without it, any co-resident guest that can reach this
		// IP could call /toolbox/exec unauthenticated. See auth.go.
		Handler:           authMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		// The toolbox exec endpoint can legitimately run for minutes. It enforces
		// its own per-command timeout, so the server write side must not impose a
		// shorter blanket deadline.
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "sandbox-agent: http listener failed: %v\n", err)
		}
	}()
	fmt.Fprintf(os.Stderr, "sandbox-agent: http listening on :%d\n", httpListenPort)
	return srv
}

// handleHealth is the liveness signal the host's per-VM probe loop
// hits every 20 seconds. Returns 204 No Content when the agent is
// alive — no body needed, just the response code says "process is
// running, scheduler has CPU, write side of the TCP socket works."
//
// 200 vs 204: 204 because there's intentionally no body. Probes that
// expect a body (or check Content-Length > 0) catch the case where
// the agent is half-alive and answering with stale cached data.
// Matches e2b's pattern (their /health is 204 too).
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func handleHTTPToolboxRPC(method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPToolboxBody+1))
		if err != nil {
			http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
			return
		}
		if len(body) > maxHTTPToolboxBody {
			http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
			return
		}

		resp := handleRequest(rpcRequest{Method: method, Params: json.RawMessage(body)})
		status := http.StatusOK
		if resp.Error != "" {
			status = http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
