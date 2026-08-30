package v1

// Sandbox content-search + global-replace (#290) — ConnectRPC.
//
// Migrated from raw mux handlers. Auth + tenant/instance ownership are enforced
// by the AgentsService interceptor chain; these methods only carry the logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// ContentSearch greps file contents inside a sandbox (ripgrep, grep fallback).
func (s *Server) ContentSearch(
	ctx context.Context,
	req *connect.Request[agentspb.ContentSearchRequest],
) (*connect.Response[agentspb.ContentSearchResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
	}
	msg := req.Msg
	if msg.GetPattern() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pattern is required"))
	}
	_, sessionID, err := s.resolveSandbox(msg.GetSandboxId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	searchPath := msg.GetPath()
	if searchPath == "" {
		searchPath = "."
	}

	// ripgrep --json when available, grep fallback. Cap at 500 matches / 8MB.
	rgCmd := fmt.Sprintf(
		`if command -v rg >/dev/null 2>&1; then `+
			`rg --json -n -m 500 %q %q 2>/dev/null | head -c 8388608; `+
			`else grep -rn --include="*" -m 500 %q %q 2>/dev/null | head -c 8388608; fi`,
		msg.GetPattern(), searchPath, msg.GetPattern(), searchPath,
	)

	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := s.sandboxMgr.Exec(execCtx, sessionID, sandbox.ExecRequest{
		Command:   []string{"/bin/sh", "-c", rgCmd},
		Timeout:   25,
		SilentLog: true, // code-search plumbing; not user activity
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("search failed: %w", err))
	}

	matches := make([]*agentspb.SearchMatch, 0)
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		// rg --json: one JSON object per line.
		var rgMsg map[string]interface{}
		if json.Unmarshal([]byte(line), &rgMsg) == nil {
			if rgMsg["type"] == "match" {
				if data, ok := rgMsg["data"].(map[string]interface{}); ok {
					path := ""
					if p, ok := data["path"].(map[string]interface{}); ok {
						path, _ = p["text"].(string)
					}
					lineNum := 0
					if ln, ok := data["line_number"].(float64); ok {
						lineNum = int(ln)
					}
					matchText := ""
					if lt, ok := data["lines"].(map[string]interface{}); ok {
						matchText, _ = lt["text"].(string)
					}
					matches = append(matches, &agentspb.SearchMatch{
						Path: path, Line: int32(lineNum),
						MatchText: strings.TrimRight(matchText, "\n"),
					})
				}
			}
			continue
		}
		// grep fallback: path:line:text.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 {
			lineNum, _ := strconv.Atoi(parts[1])
			matches = append(matches, &agentspb.SearchMatch{
				Path: parts[0], Line: int32(lineNum),
				MatchText: strings.TrimRight(parts[2], "\n"),
			})
		}
	}

	return connect.NewResponse(&agentspb.ContentSearchResponse{
		Matches: matches,
		Total:   int32(len(matches)),
		Pattern: msg.GetPattern(),
		Path:    searchPath,
	}), nil
}

// GlobalReplace performs find-and-replace across files in a sandbox.
func (s *Server) GlobalReplace(
	ctx context.Context,
	req *connect.Request[agentspb.GlobalReplaceRequest],
) (*connect.Response[agentspb.GlobalReplaceResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
	}
	msg := req.Msg
	if msg.GetPattern() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pattern is required"))
	}
	_, sessionID, err := s.resolveSandbox(msg.GetSandboxId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	searchPath := msg.GetPath()
	if searchPath == "" {
		searchPath = "."
	}

	var cmd string
	if msg.GetDryRun() {
		// Preview: show matches without modifying files.
		cmd = fmt.Sprintf(`grep -rn %q %q 2>/dev/null | head -200`, msg.GetPattern(), searchPath)
	} else {
		// Apply: perl in-place regex replace (portable, no GNU sed extensions).
		cmd = fmt.Sprintf(
			`find %q -type f | xargs -r perl -pi -e 's/%s/%s/g' 2>/dev/null && echo "done"`,
			searchPath, msg.GetPattern(), msg.GetReplacement(),
		)
	}

	execCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := s.sandboxMgr.Exec(execCtx, sessionID, sandbox.ExecRequest{
		Command:   []string{"/bin/sh", "-c", cmd},
		Timeout:   55,
		SilentLog: true, // search-and-replace plumbing; not user activity
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("replace failed: %w", err))
	}

	return connect.NewResponse(&agentspb.GlobalReplaceResponse{
		DryRun:      msg.GetDryRun(),
		Pattern:     msg.GetPattern(),
		Replacement: msg.GetReplacement(),
		Path:        searchPath,
		Output:      strings.TrimSpace(result.Stdout),
		ExitCode:    int32(result.ExitCode),
	}), nil
}
