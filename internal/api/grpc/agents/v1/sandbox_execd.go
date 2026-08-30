package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// resolveSandbox resolves a sandbox_id (ID or name) to an instance and its session ID.
func (s *Server) resolveSandbox(sandboxID string) (*sandbox.Instance, string, error) {
	if s.sandboxMgr == nil {
		return nil, "", fmt.Errorf("sandbox feature is not enabled")
	}
	inst, ok := s.sandboxMgr.GetBySandboxIDOrName(sandboxID)
	if !ok {
		return nil, "", fmt.Errorf("sandbox not found: %s", sandboxID)
	}
	return inst, inst.Config.SessionID, nil
}

// sandboxNotEnabled writes a 503 JSON error.
func sandboxNotEnabled(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]string{"error": "sandbox feature is not enabled"})
}

// sandboxJSONError writes a JSON error response.
func sandboxJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// -------------------------------------------------------------------
// Health
// -------------------------------------------------------------------

// HandleSandboxPing checks if a sandbox is reachable.
// GET /v1/sandbox/{sandbox_id}/ping
func (s *Server) HandleSandboxPing(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	inst, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	result, err := s.sandboxMgr.Exec(r.Context(), sessionID, sandbox.ExecRequest{
		Command: []string{"echo", "pong"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"sandbox_id": inst.ID,
		"exit_code":  result.ExitCode,
	})
}

// -------------------------------------------------------------------
// Command Execution
// -------------------------------------------------------------------

// HandleSandboxCommand executes a command in a sandbox via SSE stream.
// POST /v1/sandbox/{sandbox_id}/command
func (s *Server) HandleSandboxCommand(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Command    string `json:"command"`
		CWD        string `json:"cwd"`
		Background bool   `json:"background"`
		Timeout    int    `json:"timeout"` // milliseconds
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sandboxJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Command == "" {
		sandboxJSONError(w, http.StatusBadRequest, "command is required")
		return
	}

	timeout := 30 * time.Second
	if body.Timeout > 0 {
		timeout = time.Duration(body.Timeout) * time.Millisecond
	}

	execReq := sandbox.ExecRequest{
		Command: []string{"sh", "-c", body.Command},
		WorkDir: body.CWD,
		Timeout: timeout,
	}

	// Background commands: return immediately with cmd ID.
	if body.Background {
		if s.commandTracker == nil {
			sandboxJSONError(w, http.StatusInternalServerError, "command tracking not available")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmdID := s.commandTracker.Track(sandboxID, body.Command, body.CWD, cancel)

		go func() {
			defer cancel()
			result, err := s.sandboxMgr.Exec(ctx, sessionID, execReq)
			exitCode := -1
			if err == nil {
				exitCode = result.ExitCode
				if result.Stdout != "" {
					for _, line := range strings.Split(result.Stdout, "\n") {
						s.commandTracker.AppendLog(cmdID, line)
					}
				}
				if result.Stderr != "" {
					for _, line := range strings.Split(result.Stderr, "\n") {
						s.commandTracker.AppendLog(cmdID, line)
					}
				}
			}
			s.commandTracker.Finish(cmdID, exitCode)
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     cmdID,
			"status": "running",
		})
		return
	}

	// Foreground: SSE stream.
	flusher, ok := w.(http.Flusher)
	if !ok {
		sandboxJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	result, err := s.sandboxMgr.Exec(r.Context(), sessionID, execReq)
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "error", "data": err.Error()}))
		flusher.Flush()
		return
	}

	if result.Stdout != "" {
		for _, line := range strings.Split(result.Stdout, "\n") {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "stdout", "data": line}))
		}
	}
	if result.Stderr != "" {
		for _, line := range strings.Split(result.Stderr, "\n") {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "stderr", "data": line}))
		}
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]interface{}{"type": "exit", "data": strconv.Itoa(result.ExitCode)}))
	flusher.Flush()
}

// HandleSandboxCommandInterrupt interrupts a running background command.
// DELETE /v1/sandbox/{sandbox_id}/command
func (s *Server) HandleSandboxCommandInterrupt(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	if _, _, err := s.resolveSandbox(sandboxID); err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		CommandID string `json:"command_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CommandID == "" {
		sandboxJSONError(w, http.StatusBadRequest, "command_id is required")
		return
	}

	if s.commandTracker == nil || !s.commandTracker.Interrupt(body.CommandID) {
		sandboxJSONError(w, http.StatusNotFound, "command not found or already finished")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "interrupted"})
}

// HandleSandboxCommandStatus returns the status of a background command.
// GET /v1/sandbox/{sandbox_id}/command/status/{cmd_id}
func (s *Server) HandleSandboxCommandStatus(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	vars := mux.Vars(r)
	if _, _, err := s.resolveSandbox(vars["sandbox_id"]); err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	if s.commandTracker == nil {
		sandboxJSONError(w, http.StatusNotFound, "command tracking not available")
		return
	}

	cmd, ok := s.commandTracker.Get(vars["cmd_id"])
	if !ok {
		sandboxJSONError(w, http.StatusNotFound, "command not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cmd)
}

// HandleSandboxCommandLogs returns the buffered logs for a background command.
// GET /v1/sandbox/{sandbox_id}/command/{cmd_id}/logs
func (s *Server) HandleSandboxCommandLogs(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	vars := mux.Vars(r)
	if _, _, err := s.resolveSandbox(vars["sandbox_id"]); err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	if s.commandTracker == nil {
		sandboxJSONError(w, http.StatusNotFound, "command tracking not available")
		return
	}

	logs, ok := s.commandTracker.GetLogs(vars["cmd_id"])
	if !ok {
		sandboxJSONError(w, http.StatusNotFound, "command not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": logs})
}

// -------------------------------------------------------------------
// Code Execution
// -------------------------------------------------------------------

// HandleCreateCodeContext creates a persistent REPL session.
// POST /v1/sandbox/{sandbox_id}/code/context
func (s *Server) HandleCreateCodeContext(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Language == "" {
		sandboxJSONError(w, http.StatusBadRequest, "language is required")
		return
	}

	if s.codeContextMgr == nil {
		sandboxJSONError(w, http.StatusInternalServerError, "code context manager not available")
		return
	}

	cc, err := s.codeContextMgr.Create(r.Context(), sessionID, body.Language)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         cc.ID,
		"language":   cc.Language,
		"sandbox_id": cc.SandboxID,
		"created_at": cc.CreatedAt,
	})
}

// HandleListCodeContexts lists code contexts for a sandbox.
// GET /v1/sandbox/{sandbox_id}/code/contexts
func (s *Server) HandleListCodeContexts(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	if s.codeContextMgr == nil {
		sandboxJSONError(w, http.StatusInternalServerError, "code context manager not available")
		return
	}

	language := r.URL.Query().Get("language")
	contexts, err := s.codeContextMgr.List(r.Context(), sessionID, language)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]map[string]interface{}, 0, len(contexts))
	for _, cc := range contexts {
		items = append(items, map[string]interface{}{
			"id":         cc.ID,
			"language":   cc.Language,
			"sandbox_id": cc.SandboxID,
			"created_at": cc.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"contexts": items})
}

// HandleGetCodeContext returns a single code context.
// GET /v1/sandbox/{sandbox_id}/code/contexts/{context_id}
func (s *Server) HandleGetCodeContext(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	vars := mux.Vars(r)
	if _, _, err := s.resolveSandbox(vars["sandbox_id"]); err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	if s.codeContextMgr == nil {
		sandboxJSONError(w, http.StatusInternalServerError, "code context manager not available")
		return
	}

	cc, err := s.codeContextMgr.Get(r.Context(), vars["context_id"])
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         cc.ID,
		"language":   cc.Language,
		"sandbox_id": cc.SandboxID,
		"created_at": cc.CreatedAt,
	})
}

// HandleDeleteCodeContext deletes a single code context.
// DELETE /v1/sandbox/{sandbox_id}/code/contexts/{context_id}
func (s *Server) HandleDeleteCodeContext(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	vars := mux.Vars(r)
	if _, _, err := s.resolveSandbox(vars["sandbox_id"]); err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	if s.codeContextMgr == nil {
		sandboxJSONError(w, http.StatusInternalServerError, "code context manager not available")
		return
	}

	if err := s.codeContextMgr.Delete(r.Context(), vars["context_id"]); err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// HandleDeleteCodeContextsByLang deletes all code contexts for a language.
// DELETE /v1/sandbox/{sandbox_id}/code/contexts?language=python
func (s *Server) HandleDeleteCodeContextsByLang(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	if s.codeContextMgr == nil {
		sandboxJSONError(w, http.StatusInternalServerError, "code context manager not available")
		return
	}

	language := r.URL.Query().Get("language")
	count, err := s.codeContextMgr.DeleteByLanguage(r.Context(), sessionID, language)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"deleted": count})
}

// HandleExecuteCode executes code in a REPL context via SSE stream.
// POST /v1/sandbox/{sandbox_id}/code
func (s *Server) HandleExecuteCode(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		ContextID string `json:"context_id"`
		Code      string `json:"code"`
		Language  string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		sandboxJSONError(w, http.StatusBadRequest, "code is required")
		return
	}

	// If no context_id, do a one-shot execution via Exec.
	if body.ContextID == "" {
		s.handleOneShotCodeExec(w, r, sessionID, body.Code, body.Language)
		return
	}

	if s.codeContextMgr == nil {
		sandboxJSONError(w, http.StatusInternalServerError, "code context manager not available")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		sandboxJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	err = s.codeContextMgr.Execute(r.Context(), sessionID, body.ContextID, body.Code, func(evt sandbox.CodeEvent) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(evt))
		flusher.Flush()
	})
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "error", "data": err.Error()}))
		flusher.Flush()
	}
}

// handleOneShotCodeExec executes code as a one-shot command (no persistent context).
func (s *Server) handleOneShotCodeExec(w http.ResponseWriter, r *http.Request, sessionID, code, language string) {
	var cmd []string
	switch strings.ToLower(language) {
	case "python", "python3", "":
		cmd = []string{"python3", "-c", code}
	case "javascript", "node", "js":
		cmd = []string{"node", "-e", code}
	case "bash", "sh":
		cmd = []string{"bash", "-c", code}
	default:
		sandboxJSONError(w, http.StatusBadRequest, "unsupported language: "+language)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		sandboxJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	result, err := s.sandboxMgr.Exec(r.Context(), sessionID, sandbox.ExecRequest{
		Command: cmd,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "error", "data": err.Error()}))
		flusher.Flush()
		return
	}

	if result.Stdout != "" {
		for _, line := range strings.Split(result.Stdout, "\n") {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "stdout", "data": line}))
		}
	}
	if result.Stderr != "" {
		for _, line := range strings.Split(result.Stderr, "\n") {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "stderr", "data": line}))
		}
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]interface{}{"type": "exit", "data": strconv.Itoa(result.ExitCode)}))
	flusher.Flush()
}

// HandleInterruptCode interrupts a running code execution (placeholder for future use).
// DELETE /v1/sandbox/{sandbox_id}/code
func (s *Server) HandleInterruptCode(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	// For now, code interruption is handled by the client closing the SSE connection.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// -------------------------------------------------------------------
// Filesystem
// -------------------------------------------------------------------

// HandleFileInfo returns detailed metadata for one or more paths.
// GET /v1/sandbox/{sandbox_id}/files/info?path=/repo/main.go&path=/repo/go.mod
func (s *Server) HandleFileInfo(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		sandboxJSONError(w, http.StatusBadRequest, "at least one path parameter is required")
		return
	}

	files, err := s.sandboxMgr.GetFileInfo(r.Context(), sessionID, paths)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"files": files})
}

// HandleDeleteFiles deletes one or more files.
// DELETE /v1/sandbox/{sandbox_id}/files
func (s *Server) HandleDeleteFiles(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Paths) == 0 {
		sandboxJSONError(w, http.StatusBadRequest, "paths array is required")
		return
	}

	if err := s.sandboxMgr.DeleteFile(r.Context(), sessionID, body.Paths); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// HandleFilePermissions changes file permissions.
// POST /v1/sandbox/{sandbox_id}/files/permissions
func (s *Server) HandleFilePermissions(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || body.Mode == "" {
		sandboxJSONError(w, http.StatusBadRequest, "path and mode are required")
		return
	}

	if err := s.sandboxMgr.ChmodFile(r.Context(), sessionID, body.Path, body.Mode); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleMoveFiles moves or renames a file.
// POST /v1/sandbox/{sandbox_id}/files/mv
func (s *Server) HandleMoveFiles(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Source == "" || body.Destination == "" {
		sandboxJSONError(w, http.StatusBadRequest, "source and destination are required")
		return
	}

	if err := s.sandboxMgr.MoveFile(r.Context(), sessionID, body.Source, body.Destination); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "moved"})
}

// HandleSearchFilesExecd searches for files in a sandbox (by sandbox ID).
// GET /v1/sandbox/{sandbox_id}/files/search?query=foo&path=/repo
func (s *Server) HandleSearchFilesExecd(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		sandboxJSONError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	rootPath := r.URL.Query().Get("path")
	if rootPath == "" {
		rootPath = "/repo"
	}

	files, err := s.sandboxMgr.SearchFiles(r.Context(), sessionID, rootPath, query, 50)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"files": files})
}

// HandleReplaceFileContent performs a find-and-replace in a file.
// POST /v1/sandbox/{sandbox_id}/files/replace
func (s *Server) HandleReplaceFileContent(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Path   string `json:"path"`
		Old    string `json:"old"`
		New    string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || body.Old == "" {
		sandboxJSONError(w, http.StatusBadRequest, "path and old are required")
		return
	}

	if err := s.sandboxMgr.ReplaceInFile(r.Context(), sessionID, body.Path, body.Old, body.New); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "replaced"})
}

// HandleUploadFile uploads a file to the sandbox.
// POST /v1/sandbox/{sandbox_id}/files/upload
func (s *Server) HandleUploadFile(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Support multipart or raw body with path in query param.
	path := r.URL.Query().Get("path")

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(64 << 20); err != nil { // 64MB max
			sandboxJSONError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			sandboxJSONError(w, http.StatusBadRequest, "file field is required")
			return
		}
		defer file.Close()

		if path == "" {
			path = r.FormValue("path")
		}
		if path == "" {
			path = "/repo/" + header.Filename
		}

		content, err := io.ReadAll(file)
		if err != nil {
			sandboxJSONError(w, http.StatusInternalServerError, "failed to read file: "+err.Error())
			return
		}

		if err := s.sandboxMgr.WriteFile(r.Context(), sessionID, path, content); err != nil {
			sandboxJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// Raw body upload
		if path == "" {
			sandboxJSONError(w, http.StatusBadRequest, "path query parameter is required for raw uploads")
			return
		}
		content, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			sandboxJSONError(w, http.StatusInternalServerError, "failed to read body: "+err.Error())
			return
		}
		if err := s.sandboxMgr.WriteFile(r.Context(), sessionID, path, content); err != nil {
			sandboxJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "uploaded", "path": path})
}

// HandleDownloadFile downloads a file from the sandbox as binary stream.
// GET /v1/sandbox/{sandbox_id}/files/download?path=/repo/main.go
func (s *Server) HandleDownloadFile(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		sandboxJSONError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	content, err := s.sandboxMgr.ReadFile(r.Context(), sessionID, path)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path[strings.LastIndex(path, "/")+1:]))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Write(content)
}

// HandleCreateDirectories creates one or more directories.
// POST /v1/sandbox/{sandbox_id}/directories
func (s *Server) HandleCreateDirectories(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Paths) == 0 {
		sandboxJSONError(w, http.StatusBadRequest, "paths array is required")
		return
	}

	if err := s.sandboxMgr.MkdirAll(r.Context(), sessionID, body.Paths); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "created"})
}

// HandleDeleteDirectories deletes one or more directories.
// DELETE /v1/sandbox/{sandbox_id}/directories
func (s *Server) HandleDeleteDirectories(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Paths) == 0 {
		sandboxJSONError(w, http.StatusBadRequest, "paths array is required")
		return
	}

	if err := s.sandboxMgr.DeleteDirectories(r.Context(), sessionID, body.Paths); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// -------------------------------------------------------------------
// Bulk & Content Operations (POR-79)
// -------------------------------------------------------------------


// HandleBulkUpload uploads multiple files to a sandbox in one call.
// POST /v1/sandbox/{sandbox_id}/files/bulk-upload (multipart form)
// Each file field maps to a path. The path can be specified in a "path:{filename}" form field.
func (s *Server) HandleBulkUpload(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := r.ParseMultipartForm(256 << 20); err != nil { // 256 MB max total
		sandboxJSONError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
		return
	}

	baseDir := r.FormValue("base_dir")
	if baseDir == "" {
		baseDir = "/repo"
	}

	var uploaded []string
	var failed []map[string]string

	for fieldName, fileHeaders := range r.MultipartForm.File {
		for _, fh := range fileHeaders {
			destPath := r.FormValue("path_" + fieldName)
			if destPath == "" {
				destPath = baseDir + "/" + fh.Filename
			}

			f, err := fh.Open()
			if err != nil {
				failed = append(failed, map[string]string{"file": fh.Filename, "error": err.Error()})
				continue
			}
			content, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				failed = append(failed, map[string]string{"file": fh.Filename, "error": err.Error()})
				continue
			}

			if err := s.sandboxMgr.WriteFile(r.Context(), sessionID, destPath, content); err != nil {
				failed = append(failed, map[string]string{"file": fh.Filename, "path": destPath, "error": err.Error()})
				continue
			}
			uploaded = append(uploaded, destPath)
		}
	}

	if uploaded == nil {
		uploaded = []string{}
	}
	if failed == nil {
		failed = []map[string]string{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"uploaded": uploaded,
		"failed":   failed,
		"total":    len(uploaded) + len(failed),
	})
}

// -------------------------------------------------------------------
// Metrics
// -------------------------------------------------------------------

// HandleSandboxMetrics returns a one-shot resource usage snapshot.
// GET /v1/sandbox/{sandbox_id}/metrics
func (s *Server) HandleSandboxMetrics(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	stats, err := s.sandboxMgr.Stats(r.Context(), sessionID)
	if err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleSandboxMetricsStream streams resource usage snapshots via SSE.
// GET /v1/sandbox/{sandbox_id}/metrics/watch
func (s *Server) HandleSandboxMetricsStream(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		sandboxJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			stats, err := s.sandboxMgr.Stats(r.Context(), sessionID)
			if err != nil {
				fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "error", "data": err.Error()}))
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(stats))
			flusher.Flush()
		}
	}
}

// -------------------------------------------------------------------
// TTL Renewal
// -------------------------------------------------------------------

// HandleRenewExpiration extends the sandbox idle timeout.
// POST /v1/sandbox/{sandbox_id}/renew-expiration
func (s *Server) HandleRenewExpiration(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		sandboxNotEnabled(w)
		return
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	_, sessionID, err := s.resolveSandbox(sandboxID)
	if err != nil {
		sandboxJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	var body struct {
		ExtraSeconds int `json:"extra_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExtraSeconds <= 0 {
		sandboxJSONError(w, http.StatusBadRequest, "extra_seconds must be a positive integer")
		return
	}

	if err := s.sandboxMgr.RenewExpiration(sessionID, body.ExtraSeconds); err != nil {
		sandboxJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "renewed",
		"extra_seconds": body.ExtraSeconds,
	})
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"marshal failed"}`
	}
	return string(b)
}

