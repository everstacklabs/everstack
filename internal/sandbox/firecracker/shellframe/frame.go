// Package shellframe defines the binary frame protocol used between the
// host (firecracker backend) and the guest (sandbox-agent) for an
// interactive PTY shell session over vsock.
//
// Why a frame protocol at all? The vsock conn already carries raw
// bytes. We need:
//   - Multiplexing of stdin, stdout, resize, and exit on a single
//     conn, so we don't pay for three vsock dials per shell.
//   - A unambiguous boundary for control messages (resize), since the
//     stdin byte stream can contain anything including arbitrary
//     binary.
//
// The simplest encoding that solves both is [type:1][len:4 BE][payload:len].
package shellframe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Type byte

const (
	// TypeStdin carries bytes from host → guest, written to the PTY master.
	TypeStdin Type = 0x01
	// TypeStdout carries bytes from guest → host, read from the PTY master.
	// Stdout and stderr are merged at the PTY layer — that's what makes
	// it a PTY rather than a pipe.
	TypeStdout Type = 0x02
	// TypeResize carries window-size changes from host → guest. Payload
	// is fixed 4 bytes: [rows:2 BE][cols:2 BE]. The guest forwards this
	// to the PTY via TIOCSWINSZ which delivers SIGWINCH to the foreground
	// process group — TUI tools like vim/htop redraw immediately.
	TypeResize Type = 0x03
	// TypeExit is sent guest → host once when the shell process exits.
	// Payload is 4 bytes: [exit_code:4 BE int32]. Hosts should treat
	// receipt as authoritative and close the conn; the guest closes
	// immediately after sending.
	TypeExit Type = 0x04
	// TypeScrollback is sent guest → host immediately after the
	// shellOpenResponse ack, but only on reattach. Payload is raw PTY
	// output from `tmux capture-pane` — the last N lines of terminal
	// history. The host writes these bytes to xterm.js before switching
	// to live TypeStdout output so the user sees continuity across a
	// reconnect rather than a blank terminal.
	TypeScrollback Type = 0x05
)

// MaxPayload caps a single frame at 1 MiB. PTY chunks are typically
// 4–64 KiB; this cap defends against a wild peer claiming a huge
// length to make the receiver allocate.
const MaxPayload = 1 << 20

// ErrShortPayload is returned when a frame's claimed length exceeds MaxPayload.
var ErrShortPayload = errors.New("shellframe: payload too large")

// Frame is a single decoded message.
type Frame struct {
	Type    Type
	Payload []byte
}

// WriteFrame writes one frame to w. It does not buffer — caller is
// expected to pass a vsock conn (or an os.File / net.Conn equivalent)
// directly. Concurrent writers must serialize externally.
func WriteFrame(w io.Writer, t Type, payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("shellframe: payload %d > max %d", len(payload), MaxPayload)
	}
	var hdr [5]byte
	hdr[0] = byte(t)
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrame reads exactly one frame from r. Returns io.EOF cleanly on
// a closed conn; any other error is treated as protocol corruption.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	t := Type(hdr[0])
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > MaxPayload {
		return Frame{}, ErrShortPayload
	}
	var payload []byte
	if n > 0 {
		payload = make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: t, Payload: payload}, nil
}

// EncodeResize returns the 4-byte payload for a TypeResize frame.
func EncodeResize(rows, cols uint16) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint16(buf[0:2], rows)
	binary.BigEndian.PutUint16(buf[2:4], cols)
	return buf[:]
}

// DecodeResize parses a TypeResize payload. Returns an error on the
// wrong length rather than silently coercing — a malformed resize is
// always a peer bug worth surfacing.
func DecodeResize(payload []byte) (rows, cols uint16, err error) {
	if len(payload) != 4 {
		return 0, 0, fmt.Errorf("shellframe: resize payload len=%d, want 4", len(payload))
	}
	rows = binary.BigEndian.Uint16(payload[0:2])
	cols = binary.BigEndian.Uint16(payload[2:4])
	return rows, cols, nil
}

// ExitFlagSessionTerminated indicates the underlying tmux session is
// gone (not just the attach client). When clear, the host knows it
// can retry-attach to the same session_id; when set, the host should
// surface "your session ended" and start a new one if the user
// reconnects. Lives in the low bit of the optional flags byte (byte
// 4 of an extended TypeExit payload).
const ExitFlagSessionTerminated byte = 0x01

// EncodeExit returns the 4-byte payload for a TypeExit frame. Kept
// for callers that don't care about session-terminated semantics
// (e.g. unit tests, legacy paths). New code should prefer
// EncodeExitWithFlags so the host can distinguish "client detached"
// from "session is gone."
func EncodeExit(code int32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(code))
	return buf[:]
}

// EncodeExitWithFlags returns a 5-byte payload: 4 bytes of exit code
// followed by a single flags byte. Wire-compatible with EncodeExit
// because DecodeExit accepts both lengths — old hosts ignore the
// flags byte transparently.
func EncodeExitWithFlags(code int32, sessionTerminated bool) []byte {
	var flags byte
	if sessionTerminated {
		flags |= ExitFlagSessionTerminated
	}
	var buf [5]byte
	binary.BigEndian.PutUint32(buf[0:4], uint32(code))
	buf[4] = flags
	return buf[:]
}

// DecodeExit parses a TypeExit payload. Accepts the legacy 4-byte
// form (no flags) and the extended 5-byte form (with one flags
// byte). Anything else is malformed.
func DecodeExit(payload []byte) (int32, error) {
	if len(payload) != 4 && len(payload) != 5 {
		return 0, fmt.Errorf("shellframe: exit payload len=%d, want 4 or 5", len(payload))
	}
	return int32(binary.BigEndian.Uint32(payload[0:4])), nil
}

// DecodeExitWithFlags returns the exit code plus the flags byte.
// Falls back to flags=0 when given a legacy 4-byte payload so
// callers can use this everywhere without first checking the length.
func DecodeExitWithFlags(payload []byte) (int32, byte, error) {
	if len(payload) != 4 && len(payload) != 5 {
		return 0, 0, fmt.Errorf("shellframe: exit payload len=%d, want 4 or 5", len(payload))
	}
	code := int32(binary.BigEndian.Uint32(payload[0:4]))
	var flags byte
	if len(payload) == 5 {
		flags = payload[4]
	}
	return code, flags, nil
}
