package firecracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

func TestToolboxHTTPClientExec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/toolbox/exec" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var req toolbox.ExecRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.ID != "exec-1" || req.TimeoutMS != 1000 {
			t.Fatalf("unexpected request: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(toolboxHTTPResponse{Result: mustRawJSON(t, toolbox.ExecResponse{
			ID:         req.ID,
			ExitCode:   0,
			Stdout:     "ok",
			DurationMS: 12,
		})})
	}))
	defer srv.Close()

	c := &toolboxHTTPClient{baseURL: srv.URL, client: srv.Client()}
	result, err := c.Exec(context.Background(), toolbox.ExecRequest{ID: "exec-1", Command: []string{"true"}, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "ok" || result.DurationMs != 12 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestToolboxHTTPClientFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/toolbox/files/read":
			_ = json.NewEncoder(w).Encode(toolboxHTTPResponse{Result: mustRawJSON(t, toolbox.ReadFileResponse{
				Content: base64.StdEncoding.EncodeToString([]byte("hello")),
				Size:    5,
			})})
		case "/toolbox/files/list":
			_ = json.NewEncoder(w).Encode(toolboxHTTPResponse{Result: mustRawJSON(t, toolbox.ListFilesResponse{Files: []toolbox.FileInfo{{Name: "hello.txt", Path: "/tmp/hello.txt", Size: 5}}})})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := &toolboxHTTPClient{baseURL: srv.URL, client: srv.Client()}
	content, err := c.ReadFile(context.Background(), "/tmp/hello.txt")
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello" {
		t.Fatalf("content = %q", content)
	}
	files, err := c.ListFiles(context.Background(), "/tmp")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 1 || files[0].Name != "hello.txt" || files[0].Size != 5 {
		t.Fatalf("unexpected files: %+v", files)
	}
}

func TestToolboxHTTPClientSessions(t *testing.T) {
	var killed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/toolbox/sessions/list":
			_ = json.NewEncoder(w).Encode(toolboxHTTPResponse{Result: mustRawJSON(t, toolbox.SessionListResponse{
				NowUnix: 123,
				Sessions: []toolbox.SessionInfo{{
					ID:               "sess-a",
					Attached:         true,
					CreatedUnix:      100,
					LastActivityUnix: 120,
				}},
			})})
		case "/toolbox/sessions/kill":
			var req toolbox.SessionKillRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode kill request: %v", err)
			}
			killed = req.SessionID
			_ = json.NewEncoder(w).Encode(toolboxHTTPResponse{Result: mustRawJSON(t, "ok")})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := &toolboxHTTPClient{baseURL: srv.URL, client: srv.Client()}
	resp, err := c.ListSessionsRaw(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if resp.NowUnix != 123 || len(resp.Sessions) != 1 || resp.Sessions[0].ID != "sess-a" || !resp.Sessions[0].Attached {
		t.Fatalf("unexpected sessions: %+v", resp)
	}
	if err := c.KillSession(context.Background(), "sess-a"); err != nil {
		t.Fatalf("kill session: %v", err)
	}
	if killed != "sess-a" {
		t.Fatalf("killed = %q, want sess-a", killed)
	}
}

func TestToolboxHTTPClientRPCErrorDoesNotFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(toolboxHTTPResponse{Error: "file not found"})
	}))
	defer srv.Close()

	c := &toolboxHTTPClient{baseURL: srv.URL, client: srv.Client()}
	_, err := c.ReadFile(context.Background(), "/missing")
	if err == nil {
		t.Fatal("expected RPC error")
	}
	if errors.Is(err, ErrToolboxHTTPUnavailable) {
		t.Fatalf("RPC error should not be treated as fallbackable: %v", err)
	}
}

func TestToolboxHTTPClientProtocolErrorFallbackable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "old guest", http.StatusNotFound)
	}))
	defer srv.Close()

	c := &toolboxHTTPClient{baseURL: srv.URL, client: srv.Client()}
	_, err := c.ReadFile(context.Background(), "/missing")
	if !errors.Is(err, ErrToolboxHTTPUnavailable) {
		t.Fatalf("expected fallbackable error, got %v", err)
	}
}

func TestToolboxEndpointSessions(t *testing.T) {
	for method, want := range map[string]string{
		toolbox.MethodSessionList: "/toolbox/sessions/list",
		toolbox.MethodSessionKill: "/toolbox/sessions/kill",
	} {
		got, err := toolboxEndpoint(method)
		if err != nil {
			t.Fatalf("toolboxEndpoint(%q): %v", method, err)
		}
		if got != want {
			t.Fatalf("toolboxEndpoint(%q) = %q, want %q", method, got, want)
		}
	}
}

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw JSON: %v", err)
	}
	return b
}
