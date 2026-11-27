package tools_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	a2aclient "github.com/everstacklabs/everstack/internal/a2a/client"
	a2aserver "github.com/everstacklabs/everstack/internal/a2a/server"
	"github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	"github.com/gorilla/mux"
)

type a2aRouter struct{ *mux.Router }

func (r a2aRouter) HandleFunc(path string, fn http.HandlerFunc) { r.Router.HandleFunc(path, fn) }

type a2aTestAuth struct{}

func (a2aTestAuth) AuthenticateKey(r *http.Request) (string, string, bool) {
	return "tenant-a", "key-a", r.Header.Get("Authorization") == "Bearer fixture-token"
}

type a2aTestLookup struct{}

func (a2aTestLookup) GetAgent(_ context.Context, tenant, agent string) (string, string, bool, error) {
	return "Fixture bot", "", tenant == "tenant-a" && agent == "bot", nil
}

type a2aTestRunner struct {
	fail  bool
	calls *atomic.Int32
}

func (r a2aTestRunner) Run(_ context.Context, tenant, agent, message string) (string, error) {
	if r.calls != nil {
		r.calls.Add(1)
	}
	if tenant != "tenant-a" || agent != "bot" {
		return "", errors.New("unexpected tenant or agent")
	}
	if r.fail {
		return "", errors.New("fixture failure")
	}
	return "reply: " + message, nil
}

// Exercise the actual inbound codec, authentication and tenant lookup through
// the public synthetic-tool interceptor, rather than agreeing with a hand-made
// response fixture alone. No model call, external service or database is used.
func TestA2AClientToEverstackServer(t *testing.T) {
	for _, fail := range []bool{false, true} {
		server := a2aserver.New(a2aserver.Deps{Auth: a2aTestAuth{}, Agents: a2aTestLookup{}, Runner: a2aTestRunner{fail: fail}})
		router := a2aRouter{mux.NewRouter()}
		server.Mount(router)
		remote := httptest.NewTLSServer(router)
		interceptor := tools.NewToolInterceptor(nil)
		interceptor.RegisterHandler(&tools.A2ACallHandler{HTTPClient: remote.Client()})
		out, err := interceptor.ExecuteSyntheticTool(context.Background(), "call_external_agent", `{"endpoint":"`+remote.URL+`/a2a/agents/bot","auth_token":"fixture-token","message":"hello"}`)
		remote.Close()
		if fail {
			var taskErr *tools.A2ATaskError
			if !errors.As(err, &taskErr) || taskErr.State != "failed" || taskErr.TaskID == "" || taskErr.ContextID == "" || out != "" {
				t.Fatalf("lost failed task: %q, %v", out, err)
			}
		} else if err != nil || out != "reply: hello" {
			t.Fatalf("client/server incompatibility: %q, %v", out, err)
		}
	}
}

// Both protocols cross a real TLS connection and use the production client
// decoder. Task lookups must never cause an effectful message to be resent.
func TestA2ARealClientRoundTrip(t *testing.T) {
	testA2AClientRoundTrip(t, true)
}

// This transport exercises the same HTTP request, handler and client decoder
// without requiring permission to bind a local port.
func TestA2AInMemoryRoundTrip(t *testing.T) {
	testA2AClientRoundTrip(t, false)
}

type a2aHandlerTransport struct{ handler http.Handler }

func (rt a2aHandlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	rt.handler.ServeHTTP(recorder, req)
	return recorder.Result(), nil
}

func testA2AClientRoundTrip(t *testing.T, overTLS bool) {
	for _, protocol := range []string{a2aclient.Legacy, a2aclient.V1} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", protocol, fail), func(t *testing.T) {
				var calls atomic.Int32
				server := a2aserver.New(a2aserver.Deps{Auth: a2aTestAuth{}, Agents: a2aTestLookup{}, Runner: a2aTestRunner{fail: fail, calls: &calls}})
				router := a2aRouter{mux.NewRouter()}
				server.Mount(router)
				endpoint := "https://agent.test/a2a/agents/bot"
				httpClient := &http.Client{Transport: a2aHandlerTransport{handler: router}}
				if overTLS {
					remote := httptest.NewTLSServer(router)
					defer remote.Close()
					endpoint = remote.URL + "/a2a/agents/bot"
					httpClient = remote.Client()
				}
				client := &a2aclient.Client{HTTPClient: httpClient}
				conn := a2aclient.Connection{Endpoint: endpoint, Protocol: protocol, Token: "fixture-token"}
				if protocol == a2aclient.V1 {
					conn.Tenant = "untrusted-tenant"
				}
				result, err := client.Send(context.Background(), conn, "hello", "message-1")
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				if fail {
					if result.State != "failed" || result.TaskID == "" || result.ContextID == "" || !a2aclient.Terminal(result.State) {
						t.Fatalf("lost failure: %+v", result)
					}
				} else if result.State != "completed" || result.Text != "reply: hello" || !a2aclient.Terminal(result.State) {
					t.Fatalf("lost output: %+v", result)
				}
				taskID := result.TaskID
				if taskID == "" {
					taskID = "not-retained"
				}
				_, err = client.Get(context.Background(), conn, taskID)
				want := "rpc_-32001"
				if protocol == a2aclient.Legacy {
					want = "rpc_-32601"
				}
				if a2aclient.ErrorCode(err) != want || !a2aclient.Rejected(err) {
					t.Fatalf("get: %v, want %s", err, want)
				}
				if protocol == a2aclient.V1 {
					_, err = client.Cancel(context.Background(), conn, taskID)
					if a2aclient.ErrorCode(err) != "rpc_-32001" {
						t.Fatalf("cancel: %v", err)
					}
				}
				if calls.Load() != 1 {
					t.Fatalf("agent ran %d times, want exactly one send", calls.Load())
				}
			})
		}
	}
}
