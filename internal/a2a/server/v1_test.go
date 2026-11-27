package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/google/uuid"
)

const v1ValidMessage = `{"role":"ROLE_USER","messageId":"request-id","parts":[{"text":"hello"}]}`

func v1Request(method, params string) string {
	return `{"jsonrpc":"2.0","id":"request-1","method":"` + method + `","params":` + params + `}`
}

func TestV1DispatchAndValidation(t *testing.T) {
	send := func(message string) string { return v1Request("SendMessage", `{"message":`+message+`}`) }
	tests := []struct {
		name, body string
		code       int
	}{
		{"success", send(v1ValidMessage), 0},
		{"numeric role", send(strings.Replace(v1ValidMessage, `"ROLE_USER"`, `1`, 1)), 0},
		{"escaped role", send(strings.Replace(v1ValidMessage, `ROLE_USER`, `ROLE_\u0055SER`, 1)), 0},
		{"no message", v1Request("SendMessage", `{}`), codeInvalidParams},
		{"null message", v1Request("SendMessage", `{"message":null}`), codeInvalidParams},
		{"missing params", `{"jsonrpc":"2.0","id":1,"method":"SendMessage"}`, codeInvalidParams},
		{"array params", v1Request("SendMessage", `[]`), codeInvalidParams},
		{"string message", v1Request("SendMessage", `{"message":"hi"}`), codeInvalidParams},
		{"missing role", send(strings.Replace(v1ValidMessage, `"role":"ROLE_USER",`, ``, 1)), codeInvalidParams},
		{"agent role", send(strings.Replace(v1ValidMessage, `ROLE_USER`, `ROLE_AGENT`, 1)), codeInvalidParams},
		{"legacy role", send(strings.Replace(v1ValidMessage, `ROLE_USER`, `user`, 1)), codeInvalidParams},
		{"numeric agent role", send(strings.Replace(v1ValidMessage, `"ROLE_USER"`, `2`, 1)), codeInvalidParams},
		{"missing message id", send(strings.Replace(v1ValidMessage, `"messageId":"request-id",`, ``, 1)), codeInvalidParams},
		{"empty message id", send(strings.Replace(v1ValidMessage, `request-id`, ` `, 1)), codeInvalidParams},
		{"missing parts", send(`{"role":"ROLE_USER","messageId":"id"}`), codeInvalidParams},
		{"empty parts", send(strings.Replace(v1ValidMessage, `[{"text":"hello"}]`, `[]`, 1)), codeInvalidParams},
		{"empty text", send(strings.Replace(v1ValidMessage, `hello`, ` `, 1)), codeInvalidParams},
		{"raw part", send(strings.Replace(v1ValidMessage, `{"text":"hello"}`, `{"raw":"eA=="}`, 1)), codeContentNotSupported},
		{"url part", send(strings.Replace(v1ValidMessage, `{"text":"hello"}`, `{"url":"https://example.com"}`, 1)), codeContentNotSupported},
		{"data part", send(strings.Replace(v1ValidMessage, `{"text":"hello"}`, `{"data":{}}`, 1)), codeContentNotSupported},
		{"legacy file part", send(strings.Replace(v1ValidMessage, `{"text":"hello"}`, `{"file":{}}`, 1)), codeContentNotSupported},
		{"mixed parts", send(strings.Replace(v1ValidMessage, `[{"text":"hello"}]`, `[{"text":"hello"},{"data":null}]`, 1)), codeContentNotSupported},
		{"ambiguous part", send(strings.Replace(v1ValidMessage, `{"text":"hello"}`, `{"text":"hello","url":"https://example.com"}`, 1)), codeContentNotSupported},
		{"missing text", send(strings.Replace(v1ValidMessage, `{"text":"hello"}`, `{}`, 1)), codeContentNotSupported},
		{"null text", send(strings.Replace(v1ValidMessage, `"hello"`, `null`, 1)), codeContentNotSupported},
		{"numeric text", send(strings.Replace(v1ValidMessage, `"hello"`, `3`, 1)), codeContentNotSupported},
		{"get", v1Request("GetTask", `{"id":"task"}`), codeTaskNotFound},
		{"cancel", v1Request("CancelTask", `{"id":"task","tenant":"other"}`), codeTaskNotFound},
		{"get no id", v1Request("GetTask", `{}`), codeInvalidParams},
		{"cancel no id", v1Request("CancelTask", `{}`), codeInvalidParams},
		{"get blank id", v1Request("GetTask", `{"id":" "}`), codeInvalidParams},
		{"get numeric id", v1Request("GetTask", `{"id":3}`), codeInvalidParams},
		{"cancel null params", v1Request("CancelTask", `null`), codeInvalidParams},
		{"stream", v1Request("SendStreamingMessage", `{}`), codeUnsupportedOperation},
		{"subscribe", v1Request("SubscribeToTask", `{}`), codeUnsupportedOperation},
		{"create push", v1Request("CreateTaskPushNotificationConfig", `{}`), codePushNotSupported},
		{"get push", v1Request("GetTaskPushNotificationConfig", `{}`), codePushNotSupported},
		{"list push", v1Request("ListTaskPushNotificationConfigs", `{}`), codePushNotSupported},
		{"delete push", v1Request("DeleteTaskPushNotificationConfig", `{}`), codePushNotSupported},
		{"unknown", v1Request("Unknown", `{}`), codeMethodNotFound},
		{"legacy method", v1Request("message/send", `{}`), codeMethodNotFound},
		{"parse", `{`, codeParseError},
		{"trailing garbage", send(v1ValidMessage) + `x`, codeParseError},
		{"two requests", send(v1ValidMessage) + send(v1ValidMessage), codeParseError},
		{"invalid version", `{"jsonrpc":"1.0","id":1,"method":"SendMessage"}`, codeInvalidRequest},
		{"missing method", `{"jsonrpc":"2.0","id":1}`, codeInvalidRequest},
		{"empty method", `{"jsonrpc":"2.0","id":1,"method":""}`, codeInvalidRequest},
		{"invalid id", `{"jsonrpc":"2.0","id":{},"method":"SendMessage"}`, codeInvalidRequest},
		{"array envelope", `[]`, codeInvalidRequest},
		{"null envelope", `null`, codeInvalidRequest},
		{"string envelope", `"request"`, codeInvalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := newTestServer(Deps{Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) { calls++; return "reply", nil }}})
			rr := recordRequest(newRouter(s), httptestRequest(tc.body, "1.0"))
			if rr.Code != http.StatusOK || rr.Header().Get("A2A-Version") != "1.0" || rr.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status/headers: %d %v", rr.Code, rr.Header())
			}
			var resp struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Error   *rpcError       `json:"error"`
				Result  json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.JSONRPC != "2.0" {
				t.Fatal(rr.Body.String())
			}
			if tc.code == 0 {
				if resp.Error != nil || calls != 1 {
					t.Fatalf("calls %d: %s", calls, rr.Body.String())
				}
			} else if resp.Error == nil || resp.Error.Code != tc.code || calls != 0 || resp.Result != nil || resp.ID == nil {
				t.Fatalf("want code %d, no runs and explicit id; calls %d: %s", tc.code, calls, rr.Body.String())
			}
		})
	}
}

func TestV1Configuration(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		code         int
	}{
		{"blocking", `{"returnImmediately":false}`, 0},
		{"immediate", `{"returnImmediately":true,"acceptedOutputModes":["text/plain"]}`, 0},
		{"mixed modes", `{"acceptedOutputModes":["image/png","text/plain"]}`, 0},
		{"no text mode", `{"acceptedOutputModes":["image/png"]}`, codeContentNotSupported},
		{"empty modes", `{"acceptedOutputModes":[]}`, codeContentNotSupported},
		{"invalid modes", `{"acceptedOutputModes":"text/plain"}`, codeInvalidParams},
		{"invalid immediate", `{"returnImmediately":"yes"}`, codeInvalidParams},
		{"push", `{"taskPushNotificationConfig":{}}`, codePushNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := newTestServer(Deps{Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) { calls++; return "ok", nil }}})
			rr := recordRequest(newRouter(s), httptestRequest(v1Request("SendMessage", `{"message":`+v1ValidMessage+`,"configuration":`+tc.config+`}`), "1.0"))
			var resp rpcResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if tc.code == 0 {
				if resp.Error != nil || calls != 1 {
					t.Fatal(rr.Body.String())
				}
			} else if resp.Error == nil || resp.Error.Code != tc.code || calls != 0 {
				t.Fatalf("runs %d, %s", calls, rr.Body.String())
			}
		})
	}
}

func TestV1RunOutcomeAndContext(t *testing.T) {
	for _, fail := range []bool{false, true} {
		for _, contextID := range []string{"", "existing-context"} {
			t.Run(strings.Join([]string{map[bool]string{false: "success", true: "failure"}[fail], contextID}, "/"), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				deadline, _ := ctx.Deadline()
				s := newTestServer(Deps{Auth: fakeAuth{tenant: "tenant-A", apiKeyID: "key-1", ok: true}, Publisher: fakePublisher{true}, Runner: stubRunner{fn: func(runCtx context.Context, tenant, agent, msg string) (string, error) {
					gotDeadline, _ := runCtx.Deadline()
					if tenant != "tenant-A" || agent != "agent-1" || msg != "hello world" || contextkeys.GetAPIKeyID(runCtx) != "key-1" || gotDeadline != deadline {
						t.Fatalf("incorrect runner context: %s %s %q %v", tenant, agent, msg, gotDeadline)
					}
					if fail {
						return "partial secret", errors.New("internal database secret")
					}
					return "agent output", nil
				}}})
				message := strings.Replace(v1ValidMessage, `[{"text":"hello"}]`, `[{"text":"hello"},{"text":" world"}]`, 1)
				message = strings.TrimSuffix(message, "}") + `,"contextId":"` + contextID + `"}`
				req := httptestRequest(v1Request("SendMessage", `{"message":`+message+`,"tenant":"attacker","metadata":{"extra":"ok"}}`), "1.0").WithContext(ctx)
				rr := recordRequest(newRouter(s), req)
				var resp struct {
					Result v1SendResult `json:"result"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				msg := resp.Result.Message
				wantText := "agent output"
				if fail {
					task := resp.Result.Task
					if task == nil || msg != nil || task.Status.State != "TASK_STATE_FAILED" {
						t.Fatal(rr.Body.String())
					}
					if _, err := uuid.Parse(task.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := time.Parse(time.RFC3339Nano, task.Status.Timestamp); err != nil || !strings.HasSuffix(task.Status.Timestamp, "Z") {
						t.Fatalf("invalid UTC timestamp %q", task.Status.Timestamp)
					}
					msg = task.Status.Message
					wantText = v1RunFailureText
					if task.ContextID != msg.ContextID || strings.Contains(rr.Body.String(), "secret") {
						t.Fatal(rr.Body.String())
					}
				} else if resp.Result.Task != nil {
					t.Fatal(rr.Body.String())
				}
				if msg == nil || msg.Role != "ROLE_AGENT" || len(msg.Parts) != 1 || msg.Parts[0].Text != wantText {
					t.Fatal(rr.Body.String())
				}
				if _, err := uuid.Parse(msg.MessageID); err != nil {
					t.Fatal(err)
				}
				if contextID != "" {
					if msg.ContextID != contextID {
						t.Fatal(rr.Body.String())
					}
				} else if _, err := uuid.Parse(msg.ContextID); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(rr.Body.String(), `"kind"`) {
					t.Fatal("legacy discriminator in 1.0 response")
				}
			})
		}
	}
}

func TestBindingVersionSelection(t *testing.T) {
	for _, tc := range []struct {
		version, method string
		code            int
		runs            bool
	}{
		{"1.0", "SendMessage", 0, true}, {"1.0.7", "SendMessage", 0, true},
		{"", "SendMessage", codeMethodNotFound, false}, {"0.2", "SendMessage", codeMethodNotFound, false}, {"0.3", "SendMessage", codeMethodNotFound, false},
		{"", "message/send", 0, true}, {"0.2", "message/send", 0, true}, {"0.3", "message/send", 0, true}, {"0.2.5", "message/send", 0, true},
		{"2.0", "SendMessage", codeVersionNotSupported, false}, {"1.1", "SendMessage", codeVersionNotSupported, false}, {"garbage", "message/send", codeVersionNotSupported, false},
		{"1.0.x", "SendMessage", codeVersionNotSupported, false}, {"1.0.1.2", "SendMessage", codeVersionNotSupported, false},
	} {
		t.Run(tc.version+"/"+tc.method, func(t *testing.T) {
			calls := 0
			s := newTestServer(Deps{Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) { calls++; return "ok", nil }}})
			rr := recordRequest(newRouter(s), httptestRequest(v1Request(tc.method, `{"message":`+v1ValidMessage+`}`), tc.version))
			var resp rpcResponse
			_ = json.Unmarshal(rr.Body.Bytes(), &resp)
			if tc.code != 0 {
				if resp.Error == nil || resp.Error.Code != tc.code {
					t.Fatal(rr.Body.String())
				}
			} else if resp.Error != nil {
				t.Fatal(rr.Body.String())
			}
			if (calls == 1) != tc.runs {
				t.Fatalf("runs %d", calls)
			}
			if bindingVersion(tc.version) == "1.0" && rr.Header().Get("A2A-Version") != "1.0" {
				t.Fatal(rr.Header())
			}
		})
	}
}

func TestV1HTTPGates(t *testing.T) {
	for _, tc := range []struct {
		name              string
		authed, published bool
		tenant, method    string
		status            int
	}{
		{"auth", false, true, "tenant-A", http.MethodPost, http.StatusUnauthorized},
		{"empty tenant", true, true, "", http.MethodPost, http.StatusUnauthorized},
		{"unpublished", true, false, "tenant-A", http.MethodPost, http.StatusNotFound},
		{"method", true, true, "tenant-A", http.MethodGet, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Deps{Auth: fakeAuth{tenant: tc.tenant, ok: true}, Publisher: fakePublisher{tc.published}, Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) {
				t.Fatal("runner called before gates")
				return "", nil
			}}})
			req := httptestRequest(v1Request("SendMessage", `{"message":`+v1ValidMessage+`}`), "1.0")
			req.Method = tc.method
			if !tc.authed {
				req.Header.Del("Authorization")
			}
			rr := recordRequest(newRouter(s), req)
			if rr.Code != tc.status || rr.Header().Get("A2A-Version") != "1.0" {
				t.Fatalf("%d %v", rr.Code, rr.Header())
			}
		})
	}
}

func TestDualVersionCard(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{name: "Bot", found: true}, PublicURL: "https://agents.example.com/"})
	var first string
	for _, route := range []string{"agent.json", "agent-card.json"} {
		rr := doReq(t, newRouter(s), http.MethodGet, "/a2a/agents/agent-1/.well-known/"+route, true, "")
		var card AgentCard
		if err := json.Unmarshal(rr.Body.Bytes(), &card); err != nil {
			t.Fatal(err)
		}
		if card.ProtocolVersion != "0.2.5" || card.Capabilities.Streaming || card.Capabilities.PushNotifications || len(card.SupportedInterfaces) != 2 {
			t.Fatal(rr.Body.String())
		}
		for i, version := range []string{"1.0", "0.3"} {
			intf := card.SupportedInterfaces[i]
			if intf.ProtocolVersion != version || intf.ProtocolBinding != "JSONRPC" || intf.URL != "https://agents.example.com/a2a/agents/agent-1" || intf.URL != card.URL {
				t.Fatalf("bad interface: %+v", intf)
			}
		}
		if first != "" && first != rr.Body.String() {
			t.Fatal("card routes differ")
		}
		first = rr.Body.String()
	}
}

func TestRPCBodyIsBounded(t *testing.T) {
	for _, version := range []string{"", "1.0"} {
		s := New(Deps{Auth: fakeAuth{tenant: "tenant-A", ok: true}, Publisher: fakePublisher{true}, Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) {
			t.Fatal("runner called for an oversized body")
			return "", nil
		}}})
		body := `{"jsonrpc":"2.0","id":1,"method":"x","params":{"pad":"` + strings.Repeat("a", maxRPCBodyBytes) + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/a2a/agents/agent-1", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer sk-test")
		if version != "" {
			req.Header.Set("A2A-Version", version)
		}
		rec := httptest.NewRecorder()
		newRouter(s).ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), `"code":-32700`) {
			t.Fatalf("version %q: oversized body was not refused as a parse error: %.200s", version, rec.Body.String())
		}
	}
}

func TestV1SendAppliesAgentHopLimit(t *testing.T) {
	for _, tc := range []struct {
		hop     string
		runs    bool
		wantHop int
	}{
		{"", true, 1},
		{"1", true, 2},
		{"2", false, 0},
		{"banana", false, 0},
	} {
		ran := false
		gotHop := -1
		s := New(Deps{Auth: fakeAuth{tenant: "tenant-A", ok: true}, Publisher: fakePublisher{true}, Runner: stubRunner{fn: func(ctx context.Context, _, _, _ string) (string, error) {
			ran = true
			gotHop = contextkeys.AgentHop(ctx)
			return "ok", nil
		}}})
		req := httptest.NewRequest(http.MethodPost, "/a2a/agents/agent-1", strings.NewReader(v1Request("SendMessage", `{"message":`+v1ValidMessage+`}`)))
		req.Header.Set("Authorization", "Bearer sk-test")
		req.Header.Set("A2A-Version", "1.0")
		if tc.hop != "" {
			req.Header.Set(contextkeys.AgentHopHeader, tc.hop)
		}
		rec := httptest.NewRecorder()
		newRouter(s).ServeHTTP(rec, req)
		if ran != tc.runs {
			t.Fatalf("hop %q: agent ran=%v, want %v (%s)", tc.hop, ran, tc.runs, rec.Body.String())
		}
		if tc.runs && gotHop != tc.wantHop {
			t.Fatalf("hop %q: agent saw depth %d, want %d", tc.hop, gotHop, tc.wantHop)
		}
		if !tc.runs && !strings.Contains(rec.Body.String(), `"error"`) {
			t.Fatalf("hop %q: refusal is not a JSON-RPC error: %s", tc.hop, rec.Body.String())
		}
	}
}
