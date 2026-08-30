package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"github.com/everstacklabs/everstack/internal/sandbox/firecracker/shellframe"
)

// shellOpenParams is what the host sends in the JSON-RPC init message
// for method "shell_open". After the JSON response is written back the
// connection switches to the binary frame protocol.
type shellOpenParams struct {
	// SessionID, if set, reattaches to an existing tmux session. If
	// empty the agent creates a new session and returns its ID in the
	// shell_open response — clients should remember that ID and pass
	// it back on reconnect.
	SessionID string `json:"session_id"`
	// Login user. Empty / "sandbox" → spawn through `sudo -H -u sandbox`
	// so the user lands in a normal interactive shell with their own
	// $HOME, PATH, etc. Empty → root (only used as the legacy fallback
	// for rootfs builds before the sandbox user existed).
	User string `json:"user"`
	// Working directory for the spawned shell. Falls back to $HOME / "/"
	// inside the guest if empty or unusable. Only honored on session
	// creation (no-op on reattach — tmux already has a working dir).
	WorkDir string `json:"work_dir"`
	// Initial PTY size. Host will send a TypeResize frame on every
	// xterm.js resize event, but we set the initial size up-front so the
	// first prompt isn't wrapped weirdly.
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
	// Extra env vars. Merged on top of the inherited environment.
	// Only applied on session creation.
	Env map[string]string `json:"env"`
	// Shell binary. Defaults to /bin/bash (login shell). Override is here
	// for future flexibility; the host doesn't currently set it.
	Shell string `json:"shell"`
}

// shellOpenResponse is returned in the JSON-RPC envelope after a
// successful shell_open. The host stores SessionID so it can pass it
// back on reconnect.
type shellOpenResponse struct {
	SessionID string `json:"session_id"`
	// Reattached is true when the host requested a known session_id
	// and we attached to an existing one. False when we created a new
	// session. Mostly useful for telemetry / UI banners.
	Reattached bool `json:"reattached"`
}

// shellOpenErrSessionGone is the canonical error string the host
// matches against to surface "your session is gone, please start a
// new one" cleanly. Keep this in sync with the constant of the same
// name on the host side.
const shellOpenErrSessionGone = "session_gone"

// handleShellOpen takes over the vsock conn after the initial JSON
// handshake. It validates params, then defers to runShellSession for
// the actual PTY pump. The vsock-specific bits are: (a) ack via
// json.Encoder wrapping rpcResponse, (b) frames read from postInit
// (which carries any decoder.Buffered leftover bytes ahead of the
// raw conn).
//
// This handler is the legacy transport — Phase 5a adds an equivalent
// /shell WebSocket endpoint backed by the same runShellSession. The
// fcagent migrates to dialing WS in Phase 5b; vsock stays as fallback.
func handleShellOpen(conn net.Conn, postInit io.Reader, encoder *json.Encoder, raw json.RawMessage) {
	var params shellOpenParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			_ = encoder.Encode(rpcResponse{Error: "invalid shell_open params: " + err.Error()})
			return
		}
	}
	ack := func(resp *shellOpenResponse, err error) error {
		if err != nil {
			return encoder.Encode(rpcResponse{Error: err.Error()})
		}
		return encoder.Encode(rpcResponse{Result: resp})
	}
	runShellSession(params, ack, postInit, conn)
}

// runShellSession runs the tmux-attach PTY pump over a transport-
// agnostic byte stream. The ack callback is invoked exactly once
// with either a successful response (and the inner pump starts) or
// an error (and the function returns immediately). After ack, the
// transport carries shellframe binary frames in both directions:
//
//	r → frame decode → tmux attach stdin (== session stdin)
//	tmux attach stdout (== session output) → frame encode → w
//
// When r returns EOF / error, only the `tmux attach` client is
// killed via SIGHUP — the underlying tmux session keeps running so a
// future shell_open with the same session_id can reattach with its
// history, cwd, jobs, and foreground TUI intact.
//
// Concurrency: writes to w are serialized via a mutex so the stdout
// pump and the post-exit emitter can't interleave frames.
func runShellSession(params shellOpenParams, ack func(*shellOpenResponse, error) error, r io.Reader, w io.Writer) {
	if err := ensureSocketDir(); err != nil {
		_ = ack(nil, fmt.Errorf("ensure socket dir: %w", err))
		return
	}

	resolved, err := resolveOrCreateSession(params)
	if err != nil {
		_ = ack(nil, err)
		return
	}

	attachCmd, err := buildTmuxAttachCommand(resolved.SessionID)
	if err != nil {
		_ = ack(nil, err)
		return
	}

	ptmx, err := pty.StartWithSize(attachCmd, &pty.Winsize{
		Rows: nonZeroOrDefault(params.Rows, 24),
		Cols: nonZeroOrDefault(params.Cols, 80),
	})
	if err != nil {
		_ = ack(nil, fmt.Errorf("pty start (tmux attach): %w", err))
		return
	}
	defer func() { _ = ptmx.Close() }()

	// Ack BEFORE we start the pumps so the host knows the session
	// is live and can begin reading frames. If the ack itself fails
	// (transport closed before we replied), there's no point pumping
	// — return early so we don't leave the attach client running
	// with no readers.
	if err := ack(&shellOpenResponse{
		SessionID:  resolved.SessionID,
		Reattached: resolved.Reattached,
	}, nil); err != nil {
		_ = attachCmd.Process.Signal(syscall.SIGHUP)
		return
	}

	// Wait-for-exit channel. The reaper goroutine reports the attach
	// client's exit code once Wait() returns. Note that the tmux
	// attach client exiting does NOT mean the session terminated --
	// it just means this particular attachment ended (detach,
	// SIGHUP, or session_kill). The session itself may or may not
	// still exist; we re-check via sessionExists below.
	exitCh := make(chan int32, 1)
	go func() {
		err := attachCmd.Wait()
		var code int32
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = int32(ee.ExitCode())
			} else {
				code = -1
			}
		}
		exitCh <- code
	}()

	// Serialize all writes to conn. The stdout pump and the exit-frame
	// emitter both write; without the mutex their frames could interleave
	// and produce garbage on the host.
	var writeMu sync.Mutex
	writeFrame := func(t shellframe.Type, p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return shellframe.WriteFrame(w, t, p)
	}

	// On reattach, replay the last 500 lines of tmux scrollback so
	// the client's xterm.js sees continuity instead of a blank screen.
	// Sent before the live pump goroutine starts so scrollback bytes
	// arrive first. Failures are non-fatal -- a missed scrollback is
	// cosmetic, not a session-killer.
	if resolved.Reattached {
		if scrollback := captureScrollback(resolved.SessionID); len(scrollback) > 0 {
			_ = writeFrame(shellframe.TypeScrollback, scrollback)
		}
	}

	// Pump PTY master → conn as TypeStdout frames. Exits cleanly on
	// ptmx EOF (which happens when the tmux attach client exits and
	// the kernel closes the master).
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if err := writeFrame(shellframe.TypeStdout, buf[:n]); err != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Pump r → PTY master, handling resize and close inline. For the
	// vsock path, r is a MultiReader(decoder.Buffered(), conn) — the
	// JSON decoder's leftover whitespace bytes are prepended so we
	// don't lose them. For WS, r is a frame-buffering wrapper around
	// the websocket.Conn. shellframe.ReadFrame doesn't care which.
	readErr := pumpInputFrames(r, ptmx, attachCmd.Process)

	// At this point either the host closed (readErr != nil), or the
	// attach client exited (pumpDone closed because PTY hit EOF).
	// SIGHUP the attach client to ensure it tears down — the tmux
	// session itself is unaffected and remains running for future
	// reattach. The host can call session_kill explicitly if it wants
	// the session gone.
	if attachCmd.Process != nil {
		_ = attachCmd.Process.Signal(syscall.SIGHUP)
	}
	<-pumpDone

	var code int32 = -1
	select {
	case code = <-exitCh:
	default:
		// Reaper hasn't fired yet — wait briefly, but don't hang the
		// vsock conn forever if the attach is genuinely wedged.
		code = <-exitCh
	}

	// Distinguish "client detached" from "session terminated" so the
	// host can decide whether to retry-attach or surface a fresh
	// shell prompt. The session_terminated flag piggybacks on the
	// existing TypeExit frame via the shellframe encoding.
	terminated := !sessionExists(resolved.SessionID)
	_ = writeFrame(shellframe.TypeExit, shellframe.EncodeExitWithFlags(code, terminated))

	if readErr != nil && !errors.Is(readErr, io.EOF) {
		fmt.Fprintf(os.Stderr, "sandbox-agent: shell read pump: %v\n", readErr)
	}
}

// resolvedSession is the outcome of resolveOrCreateSession.
type resolvedSession struct {
	SessionID  string
	Reattached bool
}

// resolveOrCreateSession picks the tmux session this shell_open
// should attach to, given the caller's input:
//
//  1. Client-supplied session_id + already alive → reattach.
//  2. Client-supplied session_id + not alive → either resurrect it
//     under the same name (so the client's persisted ID keeps working
//     across VM reboots) or, if the caller wants strict semantics,
//     surface session_gone. We pick resurrect here because the bigger
//     UX cost is "your tab broke for no visible reason"; the trade is
//     that a session_kill followed by a reconnect will silently make
//     a fresh shell. Callers that care can poll session_list first.
//  3. No session_id → server generates one. Backwards-compat for the
//     CLI / admin UI builds that pre-date client-side ID generation.
//
// Single-flight is sessionForID's job: concurrent calls with the same
// ID converge on one tmux session, no orphans even on cold start.
func resolveOrCreateSession(p shellOpenParams) (resolvedSession, error) {
	id := p.SessionID
	clientSupplied := id != ""
	if id == "" {
		generated, err := newSessionID()
		if err != nil {
			return resolvedSession{}, fmt.Errorf("generate session id: %w", err)
		}
		id = generated
	}

	// Fast-path: client-supplied ID that already has a live tmux
	// session. Skip sessionForID's creator branch and just report
	// reattached=true.
	if clientSupplied && sessionExists(id) {
		return resolvedSession{SessionID: id, Reattached: true}, nil
	}

	resolved, err := sessionForID(id, sessionCreateParams{
		WorkDir: p.WorkDir,
		Rows:    p.Rows,
		Cols:    p.Cols,
		Env:     p.Env,
		Shell:   p.Shell,
	})
	if err != nil {
		return resolvedSession{}, fmt.Errorf("resolve session: %w", err)
	}
	// Reattached=true only when the client asked for a specific ID
	// AND we ended up reusing/resurrecting it. Server-generated IDs
	// are always fresh from the client's POV.
	return resolvedSession{SessionID: resolved, Reattached: clientSupplied}, nil
}

// buildTmuxAttachCommand returns a `tmux attach -t <id>` command
// running as the sandbox user via direct setuid (no sudo / PAM). The
// attach client gets a fresh PTY (allocated by pty.StartWithSize) and
// inherits terminal capabilities from the env we set explicitly.
//
// Why no sudo: pam_systemd / pam_env / pam_unix add 1–3s of cold
// latency per invocation, which pushes session_open past the host's
// 10-second vsock handshake deadline on the first few attempts after
// a fresh Firecracker boot. Direct setuid is ~10ms.
func buildTmuxAttachCommand(sessionID string) (*exec.Cmd, error) {
	tmuxArgs := []string{"-S", tmuxSocketPath, "attach-session", "-t", sessionID}
	cmd := exec.Command("tmux", tmuxArgs...)

	cred, home, _ := resolveSandboxUserCred()
	cmd.Env = append(os.Environ(),
		// TERM=xterm-256color is what every modern terminal expects;
		// without it tools like less and vim fall back to dumb mode and
		// look broken.
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	if home != "" {
		cmd.Env = append(cmd.Env,
			"HOME="+home,
			"USER="+sessionUser,
			"LOGNAME="+sessionUser,
		)
	}

	// Run the child in a new session so SIGHUP from us only affects the
	// tmux attach client process group, not the whole sandbox-agent.
	// The tmux server keeps running because it's daemonized and not
	// part of this process group at all.
	sysProc := &syscall.SysProcAttr{Setsid: true}
	if cred != nil {
		sysProc.Credential = cred
	}
	cmd.SysProcAttr = sysProc
	return cmd, nil
}

// pumpInputFrames is the conn → PTY direction. Returns nil when the
// host signals a clean close (EOF on conn), an error otherwise.
func pumpInputFrames(r io.Reader, ptmx *os.File, proc *os.Process) error {
	for {
		f, err := shellframe.ReadFrame(r)
		if err != nil {
			return err
		}
		switch f.Type {
		case shellframe.TypeStdin:
			if _, err := ptmx.Write(f.Payload); err != nil {
				return err
			}
		case shellframe.TypeResize:
			rows, cols, err := shellframe.DecodeResize(f.Payload)
			if err != nil {
				return err
			}
			// TIOCSWINSZ on the master delivers SIGWINCH to the
			// tmux attach client, which forwards the resize to the
			// session. With aggressive-resize on, the underlying
			// pane snaps to the new size immediately.
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		case shellframe.TypeStdout, shellframe.TypeExit:
			// These are host-receive-only types. A misbehaving peer
			// sending them isn't worth tearing down the session for —
			// just ignore.
		default:
			// Unknown type. Same reasoning: drop the frame but keep
			// the session alive so a future-version host can extend
			// the protocol without breaking old guests outright.
		}
		_ = proc // referenced for clarity; signal handling happens via the wait goroutine
	}
}

// captureScrollback returns the last 500 lines of a tmux session's
// scrollback history using `tmux capture-pane`. The output is the raw
// terminal content that was visible in the pane — suitable for replaying
// directly into xterm.js.
//
// Flags:
//   -p  print to stdout (don't create a new window)
//   -N  preserve trailing newlines
//   -J  join wrapped lines (avoids artificial line breaks at pane width)
//   -S -500  start at 500 lines above the visible screen
//   -t  target pane/session
//
// Returns nil on any error (capture is best-effort; a failed capture
// just means the user sees a blank terminal on reconnect, not a broken
// session).
func captureScrollback(sessionID string) []byte {
	out, err := tmuxAs(
		"capture-pane", "-p", "-N", "-J",
		"-S", "-500",
		"-t", sessionID,
	)
	if err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func nonZeroOrDefault(v, def uint16) uint16 {
	if v == 0 {
		return def
	}
	return v
}
