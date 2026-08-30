package v1

// Daytona-style exec sessions (process API).
//
//	POST   /v1/sandbox/instances/{id}/process/sessions                       {"session_id"?}        create
//	GET    /v1/sandbox/instances/{id}/process/sessions                                              list
//	DELETE /v1/sandbox/instances/{id}/process/sessions/{exec_session_id}                            delete
//	POST   /v1/sandbox/instances/{id}/process/sessions/{exec_session_id}/exec
//	       {"command", "run_async"?, "timeout_seconds"?, "env"?}             run a command
//	GET    .../commands/{command_id}                                          status (exit code or running)
//	GET    .../commands/{command_id}/logs                                     combined stdout+stderr
//
// A session is a directory inside the guest (/tmp/.evs-process/<id>)
// holding the session's working directory, exported environment, and
// per-command logs. Each command runs as a generated shell script that
// restores cwd+env, executes the user command, and persists the new
// cwd+env, so state carries across commands exactly like Daytona's
// sessions, with no guest-agent changes: everything rides the existing
// Exec/ReadFile/WriteFile primitives, which also means sessions survive
// gateway restarts (the guest files are the state).
//
// Async commands run under nohup; their exit code lands in a file the
// status endpoint polls. Logs combine stdout+stderr in execution order.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

const processSessionRoot = "/tmp/.evs-process"

var (
	execSessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	envKeyRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func newCommandID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "cmd_" + hex.EncodeToString(b)
}

// shellSingleQuote wraps s in single quotes, escaping embedded quotes,
// so values can be safely embedded in the generated script.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func processSessionDir(execSessionID string) string {
	return processSessionRoot + "/" + execSessionID
}

// resolveProcessRequest validates the route vars shared by the session
// endpoints. Returns ok=false after writing the error response.
func (s *Server) resolveProcessRequest(w http.ResponseWriter, r *http.Request, needSession bool) (sandboxID, execSessionID string, ok bool) {
	sandboxID, ok = s.fsRequestContext(w, r)
	if !ok {
		return "", "", false
	}
	if needSession {
		execSessionID = mux.Vars(r)["exec_session_id"]
		if !execSessionIDRe.MatchString(execSessionID) {
			writeJSONError(w, http.StatusBadRequest, "invalid session id (alphanumeric, dash, underscore; max 64)")
			return "", "", false
		}
	}
	return sandboxID, execSessionID, true
}

// HandleProcessSessions creates (POST) or lists (GET) exec sessions.
func (s *Server) HandleProcessSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleProcessSessionList(w, r)
		return
	}
	sandboxID, _, ok := s.resolveProcessRequest(w, r, false)
	if !ok {
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	// Body is optional; a missing/empty body mints a generated id.
	_ = json.NewDecoder(r.Body).Decode(&body)
	execSessionID := strings.TrimSpace(body.SessionID)
	if execSessionID == "" {
		execSessionID = "ses_" + hex.EncodeToString(func() []byte { b := make([]byte, 6); _, _ = rand.Read(b); return b }())
	}
	if !execSessionIDRe.MatchString(execSessionID) {
		writeJSONError(w, http.StatusBadRequest, "invalid session id (alphanumeric, dash, underscore; max 64)")
		return
	}
	if !s.fsExec(w, r, sandboxID, "mkdir", "-p", processSessionDir(execSessionID)) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeFSOK(w, map[string]interface{}{"session_id": execSessionID})
}

func (s *Server) handleProcessSessionList(w http.ResponseWriter, r *http.Request) {
	sandboxID, _, ok := s.resolveProcessRequest(w, r, false)
	if !ok {
		return
	}
	res, err := s.sandboxMgr.ExecBySandboxID(r.Context(), sandboxID, sandbox.ExecRequest{
		Command:   []string{"sh", "-c", "ls -1 " + processSessionRoot + " 2>/dev/null || true"},
		SilentLog: true, // process-session bookkeeping; not user activity
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	sessions := []string{}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && execSessionIDRe.MatchString(line) {
			sessions = append(sessions, line)
		}
	}
	sort.Strings(sessions)
	writeFSOK(w, map[string]interface{}{"sessions": sessions})
}

// HandleProcessSessionDelete removes a session directory (logs, env,
// cwd state). Running async commands keep running; their output files
// are unlinked but the processes are not chased (matching the
// best-effort semantics of Daytona's delete_session).
func (s *Server) HandleProcessSessionDelete(w http.ResponseWriter, r *http.Request) {
	sandboxID, execSessionID, ok := s.resolveProcessRequest(w, r, true)
	if !ok {
		return
	}
	if !s.fsExec(w, r, sandboxID, "rm", "-rf", "--", processSessionDir(execSessionID)) {
		return
	}
	writeFSOK(w, map[string]interface{}{"session_id": execSessionID, "deleted": true})
}

// HandleProcessSessionExec runs a command inside a session.
func (s *Server) HandleProcessSessionExec(w http.ResponseWriter, r *http.Request) {
	sandboxID, execSessionID, ok := s.resolveProcessRequest(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Command        string            `json:"command"`
		RunAsync       bool              `json:"run_async"`
		TimeoutSeconds int               `json:"timeout_seconds"`
		Env            map[string]string `json:"env"`
		Cwd            string            `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Command) == "" {
		writeJSONError(w, http.StatusBadRequest, "command is required")
		return
	}
	timeout := time.Duration(body.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > time.Hour {
		timeout = time.Hour
	}

	dir := processSessionDir(execSessionID)
	commandID := newCommandID()

	// Build the command script. State restore -> user command (output
	// captured) -> state persist -> exit marker. The user command is a
	// shell snippet by contract (this IS the shell API); per-request
	// env/cwd are quoted so they cannot break the scaffold.
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString(fmt.Sprintf("cd \"$(cat %s/cwd 2>/dev/null || echo \"$HOME\")\" 2>/dev/null || cd /\n", dir))
	sb.WriteString(fmt.Sprintf("[ -f %s/env ] && . %s/env 2>/dev/null\n", dir, dir))
	if body.Cwd != "" {
		sb.WriteString("cd " + shellSingleQuote(body.Cwd) + " || exit 127\n")
	}
	for k, v := range body.Env {
		if !envKeyRe.MatchString(k) {
			writeJSONError(w, http.StatusBadRequest, "invalid env key: "+k)
			return
		}
		sb.WriteString("export " + k + "=" + shellSingleQuote(v) + "\n")
	}
	sb.WriteString("{\n" + body.Command + "\n} > " + dir + "/" + commandID + ".log 2>&1\n")
	sb.WriteString("rc=$?\n")
	sb.WriteString("pwd > " + dir + "/cwd\n")
	sb.WriteString("export -p > " + dir + "/env\n")
	sb.WriteString("echo $rc > " + dir + "/" + commandID + ".exit\n")
	sb.WriteString("exit $rc\n")

	scriptPath := dir + "/" + commandID + ".sh"
	if err := s.sandboxMgr.WriteFileBySandboxID(r.Context(), sandboxID, scriptPath, []byte(sb.String())); err != nil {
		writeJSONError(w, http.StatusBadGateway, "stage command script: "+err.Error())
		return
	}

	if body.RunAsync {
		res, err := s.sandboxMgr.ExecBySandboxID(r.Context(), sandboxID, sandbox.ExecRequest{
			Command:   []string{"sh", "-c", "nohup sh " + scriptPath + " >/dev/null 2>&1 & echo $!"},
			Timeout:   10 * time.Second,
			SilentLog: true, // launcher plumbing; the user's command output is captured separately
		})
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeFSOK(w, map[string]interface{}{
			"session_id": execSessionID,
			"command_id": commandID,
			"running":    true,
			"pid":        strings.TrimSpace(res.Stdout),
		})
		return
	}

	res, err := s.sandboxMgr.ExecBySandboxID(r.Context(), sandboxID, sandbox.ExecRequest{
		Command:   []string{"sh", scriptPath},
		Timeout:   timeout,
		SilentLog: true, // runner plumbing; the user's command output is captured separately
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	output, _ := s.sandboxMgr.ReadFileBySandboxID(r.Context(), sandboxID, dir+"/"+commandID+".log")
	writeFSOK(w, map[string]interface{}{
		"session_id": execSessionID,
		"command_id": commandID,
		"exit_code":  res.ExitCode,
		"timed_out":  res.TimedOut,
		"output":     string(output),
	})
}

// HandleProcessCommandStatus reports whether a command finished and
// its exit code.
func (s *Server) HandleProcessCommandStatus(w http.ResponseWriter, r *http.Request) {
	sandboxID, execSessionID, ok := s.resolveProcessRequest(w, r, true)
	if !ok {
		return
	}
	commandID := mux.Vars(r)["command_id"]
	if !execSessionIDRe.MatchString(commandID) {
		writeJSONError(w, http.StatusBadRequest, "invalid command id")
		return
	}
	exitRaw, err := s.sandboxMgr.ReadFileBySandboxID(r.Context(), sandboxID,
		processSessionDir(execSessionID)+"/"+commandID+".exit")
	if err != nil {
		// No exit marker yet: still running (or unknown command; the
		// caller distinguishes by having received the id from exec).
		writeFSOK(w, map[string]interface{}{
			"session_id": execSessionID,
			"command_id": commandID,
			"running":    true,
		})
		return
	}
	writeFSOK(w, map[string]interface{}{
		"session_id": execSessionID,
		"command_id": commandID,
		"running":    false,
		"exit_code":  strings.TrimSpace(string(exitRaw)),
	})
}

// HandleProcessCommandLogs returns the combined stdout+stderr captured
// for a command so far.
func (s *Server) HandleProcessCommandLogs(w http.ResponseWriter, r *http.Request) {
	sandboxID, execSessionID, ok := s.resolveProcessRequest(w, r, true)
	if !ok {
		return
	}
	commandID := mux.Vars(r)["command_id"]
	if !execSessionIDRe.MatchString(commandID) {
		writeJSONError(w, http.StatusBadRequest, "invalid command id")
		return
	}
	data, err := s.sandboxMgr.ReadFileBySandboxID(r.Context(), sandboxID,
		processSessionDir(execSessionID)+"/"+commandID+".log")
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "no logs yet for this command")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}
