package tools

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	a2aclient "github.com/everstacklabs/everstack/internal/a2a/client"
)

const a2aCompletedFixture = `{"kind":"task","id":"task-1","contextId":"context-1","status":{"state":"completed"},"artifacts":[{"artifactId":"a1","parts":[{"kind":"text","text":"remote replied: hi"}]}]}`
const a2aMessageFixture = `{"kind":"message","messageId":"msg-1","role":"agent","parts":[{"kind":"text","text":"remote replied: hi"}]}`

func a2aReply(t *testing.T, w http.ResponseWriter, r *http.Request, result string) {
	t.Helper()
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": json.RawMessage(result)})
}

func TestA2ACallRequiresArgs(t *testing.T) {
	for _, args := range []map[string]interface{}{
		{"message": "hi"}, {"endpoint": "https://example.com", "message": ""},
		{"endpoint": "ftp://example.com", "message": "hi"},
		{"remote": "saved", "message": "hi"},
		{"endpoint": "https://user:secret@example.com", "message": "hi"},
		{"endpoint": "https://example.com/#secret", "message": "hi"},
		{"endpoint": "https:///missing-host", "message": "hi"},
		{"endpoint": "http://example.com", "auth_token": "secret", "message": "hi"},
	} {
		h := &A2ACallHandler{HTTPClient: &http.Client{Transport: a2aRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid arguments reached transport")
			return nil, nil
		})}}
		if _, err := h.Execute(context.Background(), args); err == nil {
			t.Errorf("expected rejection: %v", args)
		}
	}
}

func TestA2ACallRoundTrip(t *testing.T) {
	for _, fixture := range []string{a2aCompletedFixture, a2aMessageFixture} {
		t.Run(fixture[:16], func(t *testing.T) {
			var gotMethod, gotText, gotAuth string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				raw, _ := io.ReadAll(r.Body)
				var request struct {
					Method string `json:"method"`
					Params struct {
						Message struct {
							Parts []struct {
								Text string `json:"text"`
							} `json:"parts"`
						} `json:"message"`
					} `json:"params"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Error(err)
					return
				}
				gotMethod = request.Method
				if len(request.Params.Message.Parts) > 0 {
					gotText = request.Params.Message.Parts[0].Text
				}
				r.Body = io.NopCloser(strings.NewReader(string(raw)))
				a2aReply(t, w, r, fixture)
			}))
			defer srv.Close()
			h := &A2ACallHandler{HTTPClient: srv.Client()}
			out, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi there", "auth_token": "test-remote-token"})
			if err != nil || out != "remote replied: hi" {
				t.Fatalf("Execute = %q, %v", out, err)
			}
			if gotMethod != "message/send" || gotText != "hi there" || gotAuth != "Bearer test-remote-token" {
				t.Fatalf("unexpected request: %q, %q, %q", gotMethod, gotText, gotAuth)
			}
		})
	}
}

type a2aResolverFunc func(context.Context, string) (string, string, bool, error)

func (f a2aResolverFunc) Resolve(ctx context.Context, name string) (string, string, bool, error) {
	return f(ctx, name)
}

type a2aRoundTripFunc func(*http.Request) (*http.Response, error)

func (f a2aRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestA2ACallSavedRemoteCredentials(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer stored-secret" {
			t.Error("saved credential not used")
		}
		a2aReply(t, w, r, strings.ReplaceAll(a2aMessageFixture, "remote replied: hi", "stored-secret"))
	}))
	defer srv.Close()
	h := &A2ACallHandler{HTTPClient: srv.Client(), Remotes: a2aResolverFunc(func(_ context.Context, name string) (string, string, bool, error) {
		if name != "saved" {
			t.Error("wrong remote name")
		}
		return srv.URL, "stored-secret", true, nil
	})}
	out, err := h.Execute(context.Background(), map[string]interface{}{"remote": "saved", "message": "hi"})
	if err != nil || out != "[redacted]" {
		t.Fatalf("Execute=%q, %v", out, err)
	}
	for _, extra := range []map[string]interface{}{{"endpoint": srv.URL}, {"auth_token": "override"}, {"endpoint": "https://other.example", "auth_token": "override"}} {
		extra["remote"] = "saved"
		extra["message"] = "hi"
		if _, err := h.Execute(context.Background(), extra); err == nil {
			t.Fatal("accepted saved-remote override")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("override dispatched a request: calls=%d", calls.Load())
	}
	for _, found := range []bool{false, true} {
		h.Remotes = a2aResolverFunc(func(context.Context, string) (string, string, bool, error) {
			if !found {
				return "", "", false, nil
			}
			return "", "", false, errors.New("database diagnostic containing stored-secret")
		})
		if _, err := h.Execute(context.Background(), map[string]interface{}{"remote": "saved", "message": "hi"}); err == nil || strings.Contains(err.Error(), "stored-secret") {
			t.Fatalf("lookup error = %v", err)
		}
	}
}

func TestA2ACallRejectsHTTPAndProtocolFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		contentType, body string
	}{
		{"unauthorized", 401, "application/json", "secret-token"},
		{"forbidden", 403, "application/json", "secret-token"},
		{"failure", 500, "text/plain", "secret-token"},
		{"accepted", 202, "application/json", `{}`},
		{"empty", 204, "application/json", ""},
		{"html", 200, "text/html", "secret-token"},
		{"missing content type", 200, "", "{}"},
		{"sse", 200, "text/event-stream", "data: {}"},
		{"invalid envelope", 200, "application/json", `{}`},
		{"mismatched id", 200, "application/json", `{"jsonrpc":"2.0","id":"wrong","result":` + a2aMessageFixture + `}`},
		{"oversized", 200, "application/json", strings.Repeat(" ", a2aclient.MaxResponse+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			h := &A2ACallHandler{HTTPClient: &http.Client{Transport: a2aRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})}}
			out, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": "https://bot.example/?key=secret-token", "message": "hi", "auth_token": "secret-token"})
			if err == nil || out != "" {
				t.Fatalf("unexpected success: %q, %v", out, err)
			}
			if strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("credential leaked: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("retried request %d times", calls.Load())
			}
		})
	}
}

type a2aFailingBody struct{ closed bool }

func (*a2aFailingBody) Read([]byte) (int, error) {
	return 0, errors.New("secret upstream read diagnostic")
}
func (b *a2aFailingBody) Close() error { b.closed = true; return nil }

func TestA2ACallReadFailureAndTimeout(t *testing.T) {
	body := &a2aFailingBody{}
	h := &A2ACallHandler{HTTPClient: &http.Client{Transport: a2aRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body, Request: r}, nil
	})}}
	args := map[string]interface{}{"endpoint": "https://bot.example", "message": "hi"}
	if _, err := h.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "request failed") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("read failure = %v", err)
	}
	if !body.closed {
		t.Error("body not closed")
	}
	calls := 0
	h.HTTPClient = &http.Client{Transport: a2aRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil {
			t.Error("effectful POST can be replayed")
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > a2aclient.Timeout {
			t.Error("missing bounded request deadline")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Execute(ctx, args); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "do not automatically resend") {
		t.Fatalf("timeout=%v", err)
	}
	if calls != 1 {
		t.Fatalf("POST retried: %d calls", calls)
	}
	h.HTTPClient.Transport = a2aRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret transport diagnostic") })
	if _, err := h.Execute(context.Background(), args); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("transport diagnostic leaked: %v", err)
	}
}

func TestA2ACallTaskHandleRedaction(t *testing.T) {
	for _, handles := range []bool{false, true} {
		fixture := strings.Replace(a2aCompletedFixture, "completed", "working", 1)
		fixture = strings.Replace(fixture, "remote replied: hi", "secret-token", 1)
		if handles {
			fixture = strings.ReplaceAll(fixture, "task-1", "secret-token")
			fixture = strings.ReplaceAll(fixture, "context-1", "secret-token")
		}
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a2aReply(t, w, r, fixture) }))
		h := &A2ACallHandler{HTTPClient: srv.Client()}
		out, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi", "auth_token": "secret-token"})
		srv.Close()
		if out != "" || err == nil || strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("secret in task outcome: %q, %v", out, err)
		}
		if handles {
			if !strings.Contains(err.Error(), "invalid_task_id") {
				t.Fatalf("credential-bearing handle accepted: %v", err)
			}
		} else {
			var taskErr *A2ATaskError
			if !errors.As(err, &taskErr) || taskErr.PartialText != "[redacted]" {
				t.Fatalf("lost redacted task outcome: %v", err)
			}
		}
	}
}

func TestA2ACallNeverFollowsRedirects(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); a2aReply(t, w, r, a2aMessageFixture) }))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, status) }))
		h := &A2ACallHandler{HTTPClient: srv.Client()}
		out, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "private-payload", "auth_token": "private-token"})
		srv.Close()
		if err == nil || out != "" {
			t.Fatalf("redirect %d succeeded: %q, %v", status, out, err)
		}
	}
	if leaked.Load() != 0 {
		t.Fatalf("redirect target received %d requests", leaked.Load())
	}
}

func TestA2ATargetNeverReturnsMalformedURL(t *testing.T) {
	for _, endpoint := range []string{"https://example.com/?secret=credential", "https://user:credential@example.com/path", "malformed%credential"} {
		if strings.Contains(a2aTargetHost(endpoint), "credential") {
			t.Fatal("target span exposes endpoint credential")
		}
	}
}

// Guard the production construction point: a default-client injection used to
// silently disable both the SSRF boundary and the handler's timeout.
func TestA2AProductionConstructionUsesGuardedTransport(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../../../api/grpc/agents/v1/agents.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	ast.Inspect(file, func(n ast.Node) bool {
		literal, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		name, ok := literal.Type.(*ast.SelectorExpr)
		if !ok || name.Sel.Name != "A2ACallHandler" {
			return true
		}
		found++
		for _, element := range literal.Elts {
			kv, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if ok && key.Name == "HTTPClient" {
				value, isIdent := kv.Value.(*ast.Ident)
				if !isIdent || value.Name != "nil" {
					t.Error("production A2A call bypasses guarded default transport")
				}
			}
		}
		return true
	})
	if found == 0 {
		t.Fatal("production A2A registration not found; verify the new wiring")
	}
}

func TestA2AUnfinishedAndFailedTasksPreserveState(t *testing.T) {
	for _, state := range []string{"submitted", "working", "input-required", "auth-required", "failed", "canceled", "rejected", "unknown"} {
		t.Run(state, func(t *testing.T) {
			fixture := strings.Replace(a2aCompletedFixture, `"completed"`, `"`+state+`"`, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a2aReply(t, w, r, fixture) }))
			defer srv.Close()
			out, err := (&A2ACallHandler{HTTPClient: srv.Client()}).Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi"})
			var taskErr *A2ATaskError
			if out != "" || !errors.As(err, &taskErr) {
				t.Fatalf("task incorrectly succeeded: %q, %v", out, err)
			}
			if taskErr.TaskID != "task-1" || taskErr.ContextID != "context-1" || taskErr.State != state || taskErr.PartialText != "remote replied: hi" {
				t.Fatalf("lost task state: %+v", taskErr)
			}
			if !strings.Contains(err.Error(), "do not automatically resend") || strings.Contains(err.Error(), "remote replied: hi") {
				t.Fatalf("unsafe tool error: %v", err)
			}
		})
	}
}

func TestA2ACallEmptyCompletedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a2aReply(t, w, r, `{"kind":"task","id":"t","contextId":"c","status":{"state":"completed"}}`)
	}))
	defer srv.Close()
	out, err := (&A2ACallHandler{HTTPClient: srv.Client()}).Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi"})
	if out != "" || err != nil {
		t.Fatalf("empty completed task = %q, %v", out, err)
	}
}

func TestA2ACallPrivateExceptionOnlyForSavedRemote(t *testing.T) {
	endpoint := "http://10.0.0.1/a2a"
	for _, tc := range []struct {
		name, remote, endpoint string
		wantDial               bool
	}{
		{"raw exact", "", endpoint, false},
		{"saved exact", "saved", endpoint, true},
		{"saved different path", "saved", endpoint + "/other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var dials atomic.Int32
			ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{ConnectStart: func(_, _ string) {
				dials.Add(1)
				// Observe policy selection, then stop before sending a payload.
				cancel()
			}})
			h := &A2ACallHandler{PrivateEndpoints: []string{endpoint}, Remotes: a2aResolverFunc(func(context.Context, string) (string, string, bool, error) {
				return tc.endpoint, "", true, nil
			})}
			args := map[string]interface{}{"message": "hi", "endpoint": tc.endpoint}
			if tc.remote != "" {
				delete(args, "endpoint")
				args["remote"] = tc.remote
			}
			out, err := h.Execute(ctx, args)
			if err == nil || out != "" || (dials.Load() > 0) != tc.wantDial {
				t.Fatalf("out=%q err=%v dials=%d, want dial=%v", out, err, dials.Load(), tc.wantDial)
			}
		})
	}
}

func TestA2ACallErrorMapping(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"transport_error", "request failed or destination blocked; remote outcome unknown; do not automatically resend"},
		{"read_failed", "request failed or destination blocked; remote outcome unknown; do not automatically resend"},
		{"http_401", "remote rejected authentication or access (HTTP 401)"},
		{"http_403", "remote rejected authentication or access (HTTP 403)"},
		{"http_503", "remote returned HTTP 503; no completed result; do not automatically resend"},
		{"response_too_large", "response exceeds 8 MiB; no completed result; do not automatically resend"},
		{"invalid_content_type", "expected application/json; no completed result; do not automatically resend"},
		{"invalid_endpoint", "endpoint must be an absolute HTTP(S) URL without userinfo or fragment"},
		{"https_required", "bearer authentication requires an HTTPS endpoint"},
		{"invalid_envelope", "invalid remote response (invalid_envelope); no completed result; do not automatically resend"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			err := a2aCallError(context.Background(), &a2aclient.Error{Code: tc.code})
			if err.Error() != "a2a_call: "+tc.want {
				t.Fatalf("error=%v, want %s", err, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, code := range []string{"transport_error", "read_failed"} {
		err := a2aCallError(ctx, &a2aclient.Error{Code: code})
		if !errors.Is(err, context.Canceled) || err.Error() != "a2a_call: remote outcome unknown; do not automatically resend: context canceled" {
			t.Fatalf("cancellation lost: %v", err)
		}
	}
}
