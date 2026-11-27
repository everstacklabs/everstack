package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionedRunLifecycle(t *testing.T) {
	for _, protocol := range []string{Legacy, V1} {
		t.Run(protocol, func(t *testing.T) {
			var methods []string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Error("missing saved credential")
				}
				var request struct {
					ID, Method string
					Params     map[string]json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				methods = append(methods, request.Method)
				if protocol == V1 && r.Header.Get("A2A-Version") != "1.0" {
					t.Error("missing protocol header")
				}
				state := "working"
				if len(methods) == 2 {
					state = "completed"
				}
				if len(methods) == 3 {
					state = "canceled"
				}
				if len(methods) == 1 {
					var msg map[string]any
					_ = json.Unmarshal(request.Params["message"], &msg)
					if msg["messageId"] != "local-run-id" {
						t.Error("unstable message ID")
					}
					if protocol == V1 && msg["role"] != "ROLE_USER" {
						t.Error("wrong v1 role")
					}
					if protocol == Legacy && msg["kind"] != "message" {
						t.Error("wrong legacy message")
					}
					var config map[string]any
					_ = json.Unmarshal(request.Params["configuration"], &config)
					if protocol == V1 && config["returnImmediately"] != true {
						t.Error("v1 did not request task handle")
					}
				}
				var result any
				if protocol == Legacy {
					result = map[string]any{"kind": "task", "id": "task-1", "contextId": "ctx-1", "status": map[string]any{"state": state}, "artifacts": []any{map[string]any{"artifactId": "a1", "parts": []any{map[string]any{"kind": "text", "text": "answer"}}}}}
				} else {
					result = map[string]any{"id": "task-1", "contextId": "ctx-1", "status": map[string]any{"state": "TASK_STATE_" + strings.ToUpper(state)}, "artifacts": []any{map[string]any{"artifactId": "a1", "parts": []any{map[string]any{"text": "answer"}}}}}
					if len(methods) == 1 {
						result = map[string]any{"task": result}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer srv.Close()
			c := &Client{HTTPClient: srv.Client()}
			conn := Connection{Endpoint: srv.URL, Token: "fixture-secret", Protocol: protocol}
			first, err := c.Send(context.Background(), conn, "hello", "local-run-id")
			if err != nil || first.State != "working" || first.TaskID != "task-1" {
				t.Fatalf("send: %+v %v", first, err)
			}
			final, err := c.Get(context.Background(), conn, first.TaskID)
			if err != nil || final.State != "completed" || final.Text != "answer" {
				t.Fatalf("get: %+v %v", final, err)
			}
			canceled, err := c.Cancel(context.Background(), conn, first.TaskID)
			if err != nil || canceled.State != "canceled" {
				t.Fatalf("cancel: %+v %v", canceled, err)
			}
			expected := "message/send,tasks/get,tasks/cancel"
			if protocol == V1 {
				expected = "SendMessage,GetTask,CancelTask"
			}
			if strings.Join(methods, ",") != expected {
				t.Fatalf("methods=%v", methods)
			}
		})
	}
}
func TestInvalidResponsesNeverComplete(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"jsonrpc":"2.0","id":"wrong","result":{}}`, `{"jsonrpc":"2.0","id":"r","result":null}`, `{"jsonrpc":"2.0","id":"r","result":{"task":{},"message":{}}}`, `{"jsonrpc":"2.0","id":"r","result":{"task":{"id":"t","status":{"state":"working"}}}}`, `{"jsonrpc":"2.0","id":"r","result":{"task":{"id":"t","status":{"state":null}}}}`, `{"jsonrpc":"2.0","id":"r","result":{},"error":{"message":"secret"}}`} {
		if result, err := decode([]byte(raw), "r", V1, true); err == nil {
			t.Errorf("accepted %s as %+v", raw, result)
		}
	}
}
func TestClientNetworkBoundary(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	c := &Client{}
	t.Setenv("HTTP_PROXY", target.URL)
	t.Setenv("HTTPS_PROXY", target.URL)
	for _, endpoint := range []string{target.URL, strings.Replace(target.URL, "127.0.0.1", "localhost", 1)} {
		if _, err := c.Send(context.Background(), Connection{Endpoint: endpoint, Protocol: V1}, "hello", "id"); err == nil {
			t.Fatal("unguarded loopback")
		}
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c.HTTPClient = redirect.Client()
	if _, err := c.Send(context.Background(), Connection{Endpoint: redirect.URL, Protocol: V1, Token: "secret"}, "hello", "id"); err == nil {
		t.Fatal("followed redirect")
	}
	if calls.Load() != 0 {
		t.Fatal("payload reached denied target")
	}
}
func TestV1DirectMessageAndNumericalEnums(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":"r","result":{"message":{"messageId":"m","role":2,"parts":[{"text":"answer"}]}}}`
	result, err := decode([]byte(raw), "r", V1, true)
	if err != nil || result.State != "completed" || result.TaskID != "" {
		t.Fatalf("%+v %v", result, err)
	}
	raw = `{"jsonrpc":"2.0","id":"r","result":{"id":"t","status":{"state":3}}}`
	result, err = decode([]byte(raw), "r", V1, false)
	if err != nil || result.State != "completed" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestPrivateEndpointException(t *testing.T) {
	for ip, allowed := range map[string]bool{
		"10.1.2.3": true, "172.16.0.9": true, "192.168.1.1": true, "fd00::1": true, "8.8.8.8": true,
		"127.0.0.1": false, "::1": false, "169.254.169.254": false, "100.64.0.1": false, "fe80::1": false,
		"64:ff9b::a9fe:a9fe": false, "::ffff:127.0.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "2002:7f00:1::": false,
	} {
		err := privateControl("tcp", net.JoinHostPort(ip, "443"), nil)
		if (err == nil) != allowed {
			t.Errorf("privateControl(%s) err=%v, want allowed=%v", ip, err, allowed)
		}
	}
	// An approved endpoint still cannot reach loopback.
	var calls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer target.Close()
	c := &Client{PrivateEndpoints: []string{target.URL}}
	if _, err := c.Send(context.Background(), Connection{Endpoint: target.URL, Protocol: Legacy}, "hello", "id"); err == nil || calls.Load() != 0 {
		t.Fatalf("approved private endpoint reached loopback: %v", err)
	}
	// An unset allowlist splits to [""], which must not match anything.
	if c := (&Client{PrivateEndpoints: []string{""}}); c.httpClient("").Transport == privateTransport {
		t.Fatal("empty allowlist entry selected the private transport")
	}
}

const a2aCompletedFixture = `{"kind":"task","id":"task-1","contextId":"context-1","status":{"state":"completed"},"artifacts":[{"artifactId":"a1","parts":[{"kind":"text","text":"remote replied: hi"}]}]}`
const a2aMessageFixture = `{"kind":"message","messageId":"msg-1","role":"agent","parts":[{"kind":"text","text":"remote replied: hi"}]}`

func TestLegacyResultValidation(t *testing.T) {
	envelope := func(result string) string { return `{"jsonrpc":"2.0","id":"request-1","result":` + result + `}` }
	for _, tc := range []struct {
		name, raw string
		ok        bool
	}{
		{"task", envelope(a2aCompletedFixture), true},
		{"message", envelope(a2aMessageFixture), true},
		{"status text", envelope(`{"kind":"task","id":"task-1","contextId":"context-1","status":{"state":"completed","message":` + a2aMessageFixture + `}}`), true},
		{"empty completed task", envelope(`{"kind":"task","id":"task-1","contextId":"context-1","status":{"state":"completed"}}`), true},
		{"empty object", `{}`, false},
		{"null", `null`, false},
		{"array", `[]`, false},
		{"malformed", `{"jsonrpc":`, false},
		{"trailing value", envelope(a2aMessageFixture) + `{}`, false},
		{"wrong version", strings.Replace(envelope(a2aMessageFixture), `"2.0"`, `"1.0"`, 1), false},
		{"wrong id", strings.Replace(envelope(a2aMessageFixture), `"request-1"`, `"request-2"`, 1), false},
		{"numeric id", strings.Replace(envelope(a2aMessageFixture), `"request-1"`, `1`, 1), false},
		{"missing id", `{"jsonrpc":"2.0","result":` + a2aMessageFixture + `}`, false},
		{"missing result", `{"jsonrpc":"2.0","id":"request-1"}`, false},
		{"null result", envelope(`null`), false},
		{"both result and error", `{"jsonrpc":"2.0","id":"request-1","result":` + a2aMessageFixture + `,"error":null}`, false},
		{"null error", `{"jsonrpc":"2.0","id":"request-1","error":null}`, false},
		{"empty error", `{"jsonrpc":"2.0","id":"request-1","error":{}}`, false},
		{"rpc error", `{"jsonrpc":"2.0","id":"request-1","error":{"code":-32601,"message":"secret diagnostics"}}`, false},
		{"unknown kind", envelope(`{"kind":"something","parts":[]}`), false},
		{"v1 wrapper", envelope(`{"task":{"id":"1","status":{"state":"TASK_STATE_COMPLETED"}}}`), false},
		{"missing message id", envelope(strings.Replace(a2aMessageFixture, `"messageId":"msg-1",`, "", 1)), false},
		{"wrong role", envelope(strings.Replace(a2aMessageFixture, `"agent"`, `"user"`, 1)), false},
		{"empty parts", envelope(`{"kind":"message","role":"agent","messageId":"m","parts":[]}`), false},
		{"missing text", envelope(`{"kind":"message","role":"agent","messageId":"m","parts":[{"kind":"text"}]}`), false},
		{"nontext", envelope(`{"kind":"message","role":"agent","messageId":"m","parts":[{"kind":"data","data":{}}]}`), false},
		{"missing task id", envelope(strings.Replace(a2aCompletedFixture, `"id":"task-1",`, "", 1)), false},
		{"missing context id", envelope(strings.Replace(a2aCompletedFixture, `"contextId":"context-1",`, "", 1)), false},
		{"unknown state", envelope(strings.Replace(a2aCompletedFixture, `"completed"`, `"done"`, 1)), false},
		{"missing artifact id", envelope(strings.Replace(a2aCompletedFixture, `"artifactId":"a1",`, "", 1)), false},
		{"bad status message", envelope(`{"kind":"task","id":"task-1","contextId":"context-1","status":{"state":"completed","message":{}}}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := decode([]byte(tc.raw), "request-1", Legacy, true)
			if (err == nil) != tc.ok {
				t.Fatalf("result=%+v err=%v, want success=%v", result, err, tc.ok)
			}
			if !tc.ok && result.Text != "" {
				t.Fatalf("invalid response returned success text: %q", result.Text)
			}
			if err != nil && strings.Contains(err.Error(), "secret diagnostics") {
				t.Fatalf("leaked RPC diagnostics: %v", err)
			}
		})
	}
}

func TestLegacyTaskText(t *testing.T) {
	for _, tc := range []struct{ name, status, artifacts, want string }{
		{"status", a2aMessageFixture, "[]", "remote replied: hi"},
		{"artifact preferred", a2aMessageFixture, `[{"artifactId":"a","parts":[{"kind":"text","text":"artifact answer"}]}]`, "artifact answer"},
		{"empty", "null", "[]", ""},
		{"empty artifact falls back", a2aMessageFixture, `[{"artifactId":"a","parts":[{"kind":"text","text":""}]}]`, "remote replied: hi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"jsonrpc":"2.0","id":"r","result":{"kind":"task","id":"t","contextId":"c","status":{"state":"completed","message":` + tc.status + `},"artifacts":` + tc.artifacts + `}}`
			result, err := decode([]byte(raw), "r", Legacy, true)
			if err != nil || result.State != "completed" || result.Text != tc.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestLegacyInvalidStatusNeverHiddenByArtifacts(t *testing.T) {
	for _, status := range []string{`{}`, `{"kind":"message","messageId":"m","role":"agent","parts":[]}`, `{"kind":"message","messageId":"m","role":"agent","parts":[{"kind":"data","data":{}}]}`} {
		raw := strings.Replace(a2aCompletedFixture, `"state":"completed"`, `"state":"completed","message":`+status, 1)
		if result, err := decode([]byte(`{"jsonrpc":"2.0","id":"r","result":`+raw+`}`), "r", Legacy, true); err == nil || result.Text != "" {
			t.Fatalf("invalid status accepted: %+v %v", result, err)
		}
	}
}

func TestLegacyUnknownOptionalFields(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":"r","extension":true,"result":{"kind":"task","id":"t","contextId":"c","metadata":{},"status":{"state":"completed","timestamp":"now"},"artifacts":[{"artifactId":"a","name":"answer","parts":[{"kind":"text","text":"answer","metadata":{}}]}]}}`
	result, err := decode([]byte(raw), "r", Legacy, true)
	if err != nil || result.Text != "answer" {
		t.Fatalf("optional fields rejected: %+v %v", result, err)
	}
}

func TestLegacyAuthRequiredNonText(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":"r","result":{"kind":"task","id":"t","contextId":"c","status":{"state":"auth-required","message":{"kind":"message","role":"agent","messageId":"m","parts":[{"kind":"data","data":{"auth":"unsupported"}}]}}}}`
	result, err := decode([]byte(raw), "r", Legacy, true)
	if ErrorCode(err) != "unsupported_content" || result.TaskID != "t" || result.ContextID != "c" || result.State != "auth-required" || result.Text != "" {
		t.Fatalf("lost handle or accepted non-text: %+v %v", result, err)
	}
}

func TestLegacyEmptyRequestID(t *testing.T) {
	if _, err := decode([]byte(`{"jsonrpc":"2.0","id":"","result":`+a2aMessageFixture+`}`), "", Legacy, true); ErrorCode(err) != "invalid_envelope" {
		t.Fatalf("empty request ID accepted: %v", err)
	}
}

func TestLegacyMalformedRPCError(t *testing.T) {
	for _, rpcError := range []string{`null`, `{}`, `{"code":"bad","message":"secret"}`, `{"code":-1}`, `{"code":-1,"message":null}`} {
		_, err := decode([]byte(`{"jsonrpc":"2.0","id":"r","error":`+rpcError+`}`), "r", Legacy, true)
		if ErrorCode(err) != "invalid_rpc_error" {
			t.Fatalf("malformed error accepted: %v", err)
		}
	}
}

func TestClientDeadlineAndIsolation(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	for _, timeout := range []time.Duration{0, time.Second, Timeout, 2 * Timeout} {
		injected := &http.Client{Timeout: timeout, Jar: jar}
		client := (&Client{HTTPClient: injected}).httpClient("https://bot.example")
		want := timeout
		if want <= 0 || want > Timeout {
			want = Timeout
		}
		if client.Timeout != want || client.Jar != nil || client.CheckRedirect == nil {
			t.Fatalf("client not bounded/isolated: %+v", client)
		}
		if injected.Timeout != timeout || injected.Jar != jar || injected.CheckRedirect != nil {
			t.Fatal("injected client mutated")
		}
	}
	if (&Client{}).httpClient("https://bot.example").Timeout != Timeout {
		t.Fatal("production deadline changed")
	}
}

func TestGuardedTransportsBlockPrivateAndProxy(t *testing.T) {
	public, private := guardedTransports()
	if public.Proxy != nil || private.Proxy != nil {
		t.Fatal("proxy can bypass destination validation")
	}
	for _, ip := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fd00::1", "fe80::1", "0.1.2.3", "198.18.0.1", "240.0.0.1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::", "fec0::1", "255.255.255.255"} {
		conn, err := public.DialContext(context.Background(), "tcp", net.JoinHostPort(ip, "443"))
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Errorf("allowed unsafe resolved IP %s", ip)
		}
	}
}

func TestPrivateEndpointExactMatch(t *testing.T) {
	endpoint := "https://agent.internal/a2a"
	public, private := guardedTransports()
	c := &Client{PrivateEndpoints: []string{" " + endpoint + " "}}
	if c.httpClient(endpoint).Transport != private {
		t.Fatal("exact endpoint did not select private transport")
	}
	for _, other := range []string{"http://agent.internal/a2a", "https://other.internal/a2a", endpoint + "/other", endpoint + "?other=1"} {
		if c.httpClient(other).Transport != public {
			t.Fatalf("exception widened to %s", other)
		}
	}
}

func TestRejectedOnlyForDefiniteRefusals(t *testing.T) {
	for code, want := range map[string]bool{
		"http_400": true, "http_401": true, "http_404": true, "http_429": true,
		"rpc_-32700": true, "rpc_-32600": true, "rpc_-32601": true, "rpc_-32602": true,
		"rpc_-32001": true, "rpc_-32004": true, "rpc_-32099": true,
		"rpc_-32006": false, "rpc_-32603": false, "rpc_-32000": false, "rpc_-32100": false, "rpc_1": false,
		"http_408": false, "http_500": false, "http_503": false,
		"transport_error": false, "read_failed": false, "invalid_envelope": false, "remote_rpc_error": false,
	} {
		if got := Rejected(&Error{Code: code}); got != want {
			t.Errorf("Rejected(%s) = %v, want %v", code, got, want)
		}
	}
	if Rejected(nil) {
		t.Error("Rejected(nil) = true")
	}
}

func TestRPCErrorCarriesOnlyTheCode(t *testing.T) {
	for _, protocol := range []string{Legacy, V1} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "secret remote detail"}})
		}))
		c := &Client{HTTPClient: srv.Client()}
		_, err := c.Send(context.Background(), Connection{Endpoint: srv.URL, Protocol: protocol}, "hello", "msg-1")
		srv.Close()
		if ErrorCode(err) != "rpc_-32601" || !Rejected(err) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: got %v", protocol, err)
		}
	}
}

func TestRoutingTenantIsA2A1Only(t *testing.T) {
	if err := Validate(Connection{Endpoint: "https://a.example", Protocol: Legacy, Tenant: "acme"}); ErrorCode(err) != "invalid_routing_tenant" {
		t.Errorf("legacy with tenant: %v", err)
	}
	if err := Validate(Connection{Endpoint: "https://a.example", Protocol: V1, Tenant: "acme"}); err != nil {
		t.Errorf("1.0 with tenant: %v", err)
	}
}
