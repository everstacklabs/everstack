package v1

// Computer Use (POR-91..94) — ConnectRPC.
//
// Op-multiplexed: screenshot (scrot/import → image bytes), mouse/keyboard
// (xdotool), screen recording (ffmpeg). VNC (POR-94) is served separately on
// port 6080 via the preview-URL infra, not here. Auth + ownership via the
// AgentsService interceptor chain.

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

const computerUseDisplay = ":99"

// ComputerUse runs a single Computer Use op inside a sandbox.
func (s *Server) ComputerUse(
	ctx context.Context,
	req *connect.Request[agentspb.ComputerUseRequest],
) (*connect.Response[agentspb.ComputerUseResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
	}
	msg := req.Msg
	inst, ok := s.sandboxMgr.GetBySandboxIDOrName(msg.GetSandboxId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox not found: %s", msg.GetSandboxId()))
	}
	sessionID := inst.Config.SessionID
	d := computerUseDisplay

	exec := func(cmd string, timeout time.Duration) (*sandbox.ExecResult, error) {
		return s.sandboxMgr.Exec(ctx, sessionID, sandbox.ExecRequest{
			Command: []string{"/bin/sh", "-c", cmd}, Timeout: timeout,
		})
	}
	ok200 := func() *connect.Response[agentspb.ComputerUseResponse] {
		return connect.NewResponse(&agentspb.ComputerUseResponse{Status: "ok"})
	}

	switch msg.GetOp() {
	case "screenshot":
		format := msg.GetFormat()
		if format == "" {
			format = "jpeg"
		}
		quality := msg.GetQuality()
		if quality <= 0 {
			quality = 85
		}
		outFile := "/tmp/everstack-gw-screenshot." + format
		cmd := fmt.Sprintf(
			`DISPLAY=%s scrot %q 2>/dev/null || DISPLAY=%s import -window root -quality %d %q 2>/dev/null; cat %q | base64`,
			d, outFile, d, quality, outFile, outFile,
		)
		result, err := exec(cmd, 15*time.Second)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		imgData, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(result.Stdout))
		if decErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("screenshot decode failed"))
		}
		return connect.NewResponse(&agentspb.ComputerUseResponse{Image: imgData, Format: format}), nil

	case "displays":
		return connect.NewResponse(&agentspb.ComputerUseResponse{
			Displays: []*agentspb.DisplayInfo{
				{Id: 0, Display: d, Width: 1920, Height: 1080, IsPrimary: true},
			},
		}), nil

	case "mouse/click":
		btn := "1"
		switch msg.GetButton() {
		case "right":
			btn = "3"
		case "middle":
			btn = "2"
		}
		repeat := ""
		if msg.GetDoubleClick() {
			repeat = "--repeat 2"
		}
		_, _ = exec(fmt.Sprintf(`DISPLAY=%s xdotool mousemove %d %d click %s %s`,
			d, msg.GetX(), msg.GetY(), repeat, btn), 5*time.Second)
		return ok200(), nil

	case "keyboard/type":
		_, _ = exec(fmt.Sprintf(`DISPLAY=%s xdotool type --clearmodifiers %q`, d, msg.GetText()), 10*time.Second)
		return ok200(), nil

	case "keyboard/key":
		_, _ = exec(fmt.Sprintf(`DISPLAY=%s xdotool key --clearmodifiers %q`, d, msg.GetKey()), 5*time.Second)
		return ok200(), nil

	case "mouse/scroll":
		btn := "4"
		if msg.GetDirection() == "down" {
			btn = "5"
		}
		amount := msg.GetAmount()
		if amount <= 0 {
			amount = 3
		}
		_, _ = exec(fmt.Sprintf(`DISPLAY=%s xdotool mousemove %d %d click --repeat %d %s`,
			d, msg.GetX(), msg.GetY(), amount, btn), 5*time.Second)
		return ok200(), nil

	case "mouse/move":
		_, _ = exec(fmt.Sprintf(`DISPLAY=%s xdotool mousemove %d %d`, d, msg.GetX(), msg.GetY()), 5*time.Second)
		return ok200(), nil

	case "mouse/drag":
		_, _ = exec(fmt.Sprintf(`DISPLAY=%s xdotool mousemove %d %d mousedown 1 mousemove %d %d mouseup 1`,
			d, msg.GetX(), msg.GetY(), msg.GetToX(), msg.GetToY()), 10*time.Second)
		return ok200(), nil

	case "recording/start":
		fps := msg.GetFps()
		if fps <= 0 {
			fps = 15
		}
		recID := "rec_" + strconv.FormatInt(time.Now().UnixNano(), 36)
		outFile := fmt.Sprintf("/tmp/everstack-recordings/%s.mp4", recID)
		cmd := fmt.Sprintf(
			`mkdir -p /tmp/everstack-recordings && DISPLAY=%s ffmpeg -f x11grab -r %d -s 1920x1080 -i %s -codec:v libx264 -preset ultrafast %q </dev/null >/dev/null 2>&1 &`,
			d, fps, d, outFile,
		)
		_, _ = exec(cmd, 5*time.Second)
		return connect.NewResponse(&agentspb.ComputerUseResponse{RecordingId: recID, File: outFile}), nil

	case "recording/stop":
		_, _ = exec(`pkill -INT ffmpeg 2>/dev/null || true`, 5*time.Second)
		return ok200(), nil

	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown computer use operation: %s", msg.GetOp()))
	}
}

// ComputerUseInfo returns Computer Use capability info.
func (s *Server) ComputerUseInfo(
	ctx context.Context,
	req *connect.Request[agentspb.ComputerUseInfoRequest],
) (*connect.Response[agentspb.ComputerUseInfoResponse], error) {
	return connect.NewResponse(&agentspb.ComputerUseInfoResponse{
		SandboxId: req.Msg.GetSandboxId(),
		Operations: []string{
			"screenshot", "displays", "mouse/click", "mouse/move", "mouse/scroll", "mouse/drag",
			"keyboard/type", "keyboard/key", "recording/start", "recording/stop",
		},
		Note: "Requires SANDBOX_COMPUTER_USE=1 on CreateSandboxRequest. Install Xvfb, xdotool, scrot/ImageMagick, and ffmpeg in the sandbox image.",
	}), nil
}
