package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

const (
	tmuxSocketDir  = "/run/everstack"
	tmuxSocketPath = tmuxSocketDir + "/tmux.sock"
	sessionUser    = "sandbox"
	// prewarmSessionName names the always-on tmux session we spawn at
	// agent startup. Its purpose is just to keep the tmux server
	// daemon process alive (tmux exits when the last session dies),
	// so the first user-visible shell_open hits a warm server instead
	// of paying tmux-server cold-spawn latency on its critical path.
	// Filtered out of sessionList so users never see it.
	prewarmSessionName = "__everstack_prewarm__"
)

// sessionCreateParams is what the host sends to spin up a fresh tmux
// session. Mirrors shellOpenParams for the create-on-attach path; the
// host can also call session_create explicitly to pre-allocate.
type sessionCreateParams struct {
	WorkDir string            `json:"work_dir"`
	Rows    uint16            `json:"rows"`
	Cols    uint16            `json:"cols"`
	Env     map[string]string `json:"env"`
	Shell   string            `json:"shell"`
}

type sessionCreateResponse struct {
	SessionID string `json:"session_id"`
}

type sessionListResponse = toolbox.SessionListResponse
type sessionInfo = toolbox.SessionInfo
type sessionKillParams = toolbox.SessionKillRequest

// ensureSocketDir makes sure /run/everstack exists with the right
// ownership before tmux tries to bind its socket there. /run is tmpfs
// in modern systemd, so this directory disappears across boots — we
// recreate it on every sandbox-agent startup. Idempotent.
func ensureSocketDir() error {
	if err := os.MkdirAll(tmuxSocketDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", tmuxSocketDir, err)
	}
	// chown to the sandbox user so non-root tmux servers can bind here.
	// Best-effort — if the sandbox user doesn't exist (legacy rootfs)
	// we'll fall back to root-owned tmux later.
	_ = exec.Command("chown", sessionUser+":"+sessionUser, tmuxSocketDir).Run()
	_ = os.Chmod(tmuxSocketDir, 0700)
	return nil
}

// sandboxUserCredOnce resolves the sandbox user's UID/GID once and
// caches the result. user.Lookup hits /etc/passwd which is cheap, but
// shell_open is on the critical path so we cache anyway.
var (
	sandboxUserCredOnce sync.Once
	sandboxUserCred     *syscall.Credential
	sandboxUserHome     string
	sandboxUserShell    string
)

func resolveSandboxUserCred() (*syscall.Credential, string, string) {
	sandboxUserCredOnce.Do(func() {
		u, err := user.Lookup(sessionUser)
		if err != nil {
			return
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return
		}
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return
		}
		sandboxUserCred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
		sandboxUserHome = u.HomeDir
		// /etc/passwd shell — used only as informational $SHELL in env.
		// tmux still spawns its own pane shells per-config; we don't
		// exec this directly.
		sandboxUserShell = "/bin/bash"
	})
	return sandboxUserCred, sandboxUserHome, sandboxUserShell
}

// tmuxAs runs a tmux command as the sandbox user against the per-VM
// shared socket. Drops directly to the sandbox UID/GID via setuid —
// no sudo, no PAM session setup. The PAM path adds ~1–3s of cold
// latency per invocation on a fresh Firecracker boot (pam_systemd,
// pam_env, pam_unix), and session_create runs three tmux commands
// back-to-back. Going through PAM blew past the host's 10s vsock
// handshake deadline, producing the reconnect spiral where each
// failed handshake created a new orphaned tmux session.
func tmuxAs(args ...string) ([]byte, error) {
	full := append([]string{"-S", tmuxSocketPath}, args...)
	cmd := exec.Command("tmux", full...)

	if cred, home, _ := resolveSandboxUserCred(); cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
		// Set HOME + USER explicitly so tmux + downstream bash find the
		// right profile/state directories. Inherit PATH from the agent
		// (root's PATH includes everything sandbox would need).
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"USER="+sessionUser,
			"LOGNAME="+sessionUser,
		)
	}
	return cmd.CombinedOutput()
}

// newSessionID returns a 32-char hex string. Avoids tmux-reserved
// characters (':' and '.') and is short enough to appear in logs
// without wrapping.
func newSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// sessionInflight coordinates concurrent shell_open calls that target
// the same session_id. Without this guard, two WS reconnects arriving
// during a cold start (~1s) would each see "no session with this ID"
// in tmux, each call `new-session`, and produce two tmux sessions with
// the same name — at best a duplicate, at worst tmux yelling about a
// collision after one of them committed. With `LoadOrStore`, only the
// first caller actually shells out to tmux; everyone else waits on
// `entry.ready` and reuses the result. Belt + braces with `tmux
// new-session -A` (which itself is atomic at the tmux-server layer).
type sessionInflightEntry struct {
	ready chan struct{}
	id    string
	err   error
}

var sessionInflight sync.Map // map[string]*sessionInflightEntry

// sessionForID returns the canonical session for the given ID,
// creating the underlying tmux session if it doesn't already exist.
// Safe to call concurrently with the same ID from any number of
// goroutines: exactly one will run tmux; the rest will wait on a
// channel and reuse the result.
//
// id must be non-empty. Callers that want a fresh server-side ID
// should generate one via newSessionID() and pass it in.
func sessionForID(id string, params sessionCreateParams) (string, error) {
	if id == "" {
		return "", fmt.Errorf("sessionForID: id is required")
	}

	entry := &sessionInflightEntry{ready: make(chan struct{}), id: id}
	actual, loaded := sessionInflight.LoadOrStore(id, entry)
	if loaded {
		existing := actual.(*sessionInflightEntry)
		<-existing.ready
		if existing.err != nil {
			// Failed creators publish their error so concurrent
			// waiters all see the same failure. Drop the entry so
			// the next CALLER (not waiter) can retry.
			sessionInflight.CompareAndDelete(id, existing)
			return "", existing.err
		}
		return existing.id, nil
	}

	// First-creator path. Always close `ready` so waiters unblock.
	err := createTmuxSession(id, params)
	entry.err = err
	close(entry.ready)
	if err != nil {
		sessionInflight.CompareAndDelete(id, entry)
		return "", err
	}
	// Successful entries stay in the map indefinitely — sessionKill /
	// session-gone cleanup removes them. Until then, fast-path: future
	// `sessionForID` calls with the same id LoadOrStore the existing
	// entry and return immediately.
	return id, nil
}

// createTmuxSession is the single-shot tmux session creator. Uses
// `new-session -A -s <id>` (attach-or-create) so it's idempotent at
// the tmux layer in case our sessionInflight guard ever misses
// (e.g. across separate sandbox-agent processes, which shouldn't
// happen but defends against it cheaply).
//
// We do NOT attach a client here — that's what shell_open does via
// `tmux attach-session` on its own PTY. Splitting create from attach
// lets the host pre-allocate sessions (e.g. for an external SSH
// workflow) without needing a client buffer.
func createTmuxSession(id string, params sessionCreateParams) error {
	if err := ensureSocketDir(); err != nil {
		return err
	}

	shellBin := params.Shell
	if shellBin == "" {
		shellBin = "/bin/bash"
	}
	if _, err := os.Stat(shellBin); err != nil {
		shellBin = "/bin/sh"
	}

	// -A = attach-or-create; idempotent for the same session name.
	// -d = don't attach a client in this invocation.
	args := []string{"new-session", "-A", "-d", "-s", id}
	if params.WorkDir != "" {
		if info, err := os.Stat(params.WorkDir); err == nil && info.IsDir() {
			args = append(args, "-c", params.WorkDir)
		}
	}
	if params.Rows > 0 && params.Cols > 0 {
		args = append(args, "-x", fmt.Sprintf("%d", params.Cols), "-y", fmt.Sprintf("%d", params.Rows))
	}
	// `bash -l` gives the user a real login shell with profile/bashrc
	// applied.
	args = append(args, "--", shellBin, "-l")

	if out, err := tmuxAs(args...); err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Per-session options. With `-A`, if we re-entered an existing
	// session these are idempotent (set-environment overwrites,
	// set-window-option is a no-op if already set). aggressive-resize
	// makes the pane follow the active client's geometry rather than
	// the smallest attached.
	_, _ = tmuxAs("set-environment", "-t", id, "EVERSTACK_SESSION_ID", id)
	_, _ = tmuxAs("set-window-option", "-t", id, "aggressive-resize", "on")
	// Disable the tmux status bar. Users interact through xterm.js, not the
	// tmux status line, and that line (session id, host label, clock) plus
	// its once-a-second redraw gets captured into the PTY stream and teed
	// into the Logs tab as noise. Turning it off keeps the shell log clean.
	_, _ = tmuxAs("set-option", "-t", id, "status", "off")

	return nil
}

// sessionCreate is the legacy entry point that generates its own ID.
// Retained so callers (in tests, future RPC surfaces) that want a
// server-generated ID can still get one. New shell_open paths take
// the client-supplied ID and call sessionForID directly.
func sessionCreate(params sessionCreateParams) (sessionCreateResponse, error) {
	id, err := newSessionID()
	if err != nil {
		return sessionCreateResponse{}, fmt.Errorf("generate session id: %w", err)
	}
	resolved, err := sessionForID(id, params)
	if err != nil {
		return sessionCreateResponse{}, err
	}
	return sessionCreateResponse{SessionID: resolved}, nil
}

// sessionList enumerates active tmux sessions. Used by the host on
// fcagent startup to rebuild its session registry, and by the admin
// UI to render the "sessions for this sandbox" list.
//
// Returns an empty list (no error) when the tmux server isn't running
// yet — that's the steady state for a freshly booted VM nobody has
// shelled into.
func sessionList() (sessionListResponse, error) {
	// Capture the guest's wall clock once per call. Sending it back
	// alongside the per-session last_activity lets the host compute
	// "idle duration" in pure guest-clock arithmetic without needing
	// to trust either side's drift.
	nowUnix := time.Now().Unix()

	if _, err := os.Stat(tmuxSocketPath); os.IsNotExist(err) {
		return sessionListResponse{Sessions: []sessionInfo{}, NowUnix: nowUnix}, nil
	}
	out, err := tmuxAs("list-sessions", "-F", "#{session_name}|#{session_attached}|#{session_created}|#{session_activity}")
	if err != nil {
		// tmux returns "no server running" with a non-zero exit when
		// the socket exists but the server has been kill-server'd. We
		// treat that the same as "no sessions" rather than bubbling
		// the error up — the host doesn't care about the distinction.
		if strings.Contains(string(out), "no server running") {
			return sessionListResponse{Sessions: []sessionInfo{}, NowUnix: nowUnix}, nil
		}
		return sessionListResponse{}, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(string(out)))
	}

	sessions := []sessionInfo{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		// Tolerate old guests that only emit three fields — extending
		// the format spec is forward-compatible only because we read
		// the missing field as zero, which the reaper treats as
		// "unknown, do not kill."
		if len(parts) < 3 {
			continue
		}
		// Hide the boot-time prewarm session from the public list — it
		// exists only to keep the tmux daemon warm and would confuse
		// the admin UI's "active shell sessions" panel.
		if parts[0] == prewarmSessionName {
			continue
		}
		var created, activity int64
		_, _ = fmt.Sscanf(parts[2], "%d", &created)
		if len(parts) == 4 {
			_, _ = fmt.Sscanf(parts[3], "%d", &activity)
		}
		sessions = append(sessions, sessionInfo{
			ID:               parts[0],
			Attached:         parts[1] != "0",
			CreatedUnix:      created,
			LastActivityUnix: activity,
		})
	}
	return sessionListResponse{Sessions: sessions, NowUnix: nowUnix}, nil
}

// sessionKill terminates a tmux session. Idempotent — killing a
// non-existent session is not an error; it matches the desired
// post-condition. Also drops the sessionInflight entry so a future
// shell_open with the same ID creates afresh.
func sessionKill(params sessionKillParams) error {
	if params.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	// Drop inflight cache regardless of tmux outcome — the session
	// is conceptually gone after this call returns.
	defer sessionInflight.Delete(params.SessionID)
	out, err := tmuxAs("kill-session", "-t", params.SessionID)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// "can't find session" → already gone; success from the host's
		// perspective.
		if strings.Contains(msg, "can't find session") || strings.Contains(msg, "no server running") {
			return nil
		}
		return fmt.Errorf("tmux kill-session: %w: %s", err, msg)
	}
	return nil
}

// sessionExists returns true if a tmux session with the given ID is
// currently alive. Used by shell_open's reattach path so we can
// surface a typed "session is gone" error instead of letting the
// tmux attach process fail with a cryptic stderr line.
func sessionExists(id string) bool {
	if id == "" {
		return false
	}
	if _, err := os.Stat(tmuxSocketPath); os.IsNotExist(err) {
		return false
	}
	_, err := tmuxAs("has-session", "-t", id)
	return err == nil
}

// handleSessionCreate is the JSON-RPC dispatcher entry. Thin wrapper
// over sessionCreate that handles param parsing and error mapping.
func handleSessionCreate(raw json.RawMessage) rpcResponse {
	var params sessionCreateParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return rpcResponse{Error: "invalid session_create params: " + err.Error()}
		}
	}
	resp, err := sessionCreate(params)
	if err != nil {
		return rpcResponse{Error: err.Error()}
	}
	return rpcResponse{Result: resp}
}

func handleSessionList(_ json.RawMessage) rpcResponse {
	resp, err := sessionList()
	if err != nil {
		return rpcResponse{Error: err.Error()}
	}
	return rpcResponse{Result: resp}
}

func handleSessionKill(raw json.RawMessage) rpcResponse {
	var params sessionKillParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return rpcResponse{Error: "invalid session_kill params: " + err.Error()}
	}
	if err := sessionKill(params); err != nil {
		return rpcResponse{Error: err.Error()}
	}
	return rpcResponse{Result: "ok"}
}

// warmupShellEnvironment is fire-and-forget startup work that pre-pays
// the cold-start cost of opening the first interactive shell. Runs in
// a goroutine from main() right after the vsock listener is up; no
// caller waits on it.
//
// Two costs paid up front:
//
//  1. tmux server first-spawn (~50-100ms on a cold VM). We create a
//     hidden keepalive session running `sleep infinity` so the tmux
//     daemon stays alive and ready. The user's first shell_open then
//     hits a warm daemon with `new-session -A` instead of paying
//     daemon-spawn latency on the critical path.
//
//  2. bash login profile + /etc/profile.d/*.sh + ~/.bashrc page-in
//     (~200-500ms on Ubuntu 22.04). We run `bash -lc :` as the sandbox
//     user once at boot to seed the kernel page cache with every file
//     `bash -l` will touch. By the time the user's real shell starts,
//     bash, glibc, and the profile scripts are all hot.
//
// Together this should drop first-shell-paint from "several seconds"
// to "under half a second" on a healthy VM. Failures are logged and
// ignored — a failed warmup just means the first user shell pays the
// cost it always did.
func warmupShellEnvironment() {
	if err := ensureSocketDir(); err != nil {
		// /run/everstack is tmpfs; if mkdir fails, tmux's socket bind
		// will fail later too. Nothing to do here.
		return
	}

	// Step 1: spawn the tmux daemon with a keepalive session. We use
	// `new-session -A` so that if for some reason a prior agent run
	// already left a keepalive behind, we just attach (idempotent).
	// `sleep infinity` is the cheapest possible pane command — tmux
	// sees it as "running" so the server stays alive, but it never
	// consumes CPU.
	if out, err := tmuxAs(
		"new-session", "-A", "-d", "-s", prewarmSessionName,
		"--", "sleep", "infinity",
	); err != nil {
		// Don't block — log and move on. Subsequent shell_open will
		// just pay the cold-spawn cost as before.
		fmt.Fprintf(os.Stderr, "sandbox-agent warmup: tmux start failed: %v: %s\n",
			err, strings.TrimSpace(string(out)))
	}

	// Step 2: warm bash login profile. Run as the sandbox user so the
	// HOME, profile chain matches what tmux's pane command will see
	// when the user opens their shell.
	cred, home, _ := resolveSandboxUserCred()
	if cred == nil {
		return
	}
	cmd := exec.Command("/bin/bash", "-lc", ":")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"USER="+sessionUser,
		"LOGNAME="+sessionUser,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent warmup: bash -l failed: %v: %s\n",
			err, strings.TrimSpace(string(out)))
	}
}
