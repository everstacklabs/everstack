package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestA2ACallRequiresArgs(t *testing.T) {
	h := &A2ACallHandler{}
	if _, err := h.Execute(context.Background(), map[string]interface{}{"message": "hi"}); err == nil {
		t.Error("expected error without endpoint")
	}
	if _, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": "https://x", "message": ""}); err == nil {
		t.Error("expected error without message")
	}
	if _, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": "ftp://x", "message": "hi"}); err == nil {
		t.Error("expected error for non-http endpoint")
	}
}

func TestA2ACallRoundTripTask(t *testing.T) {
	var gotMethod, gotText, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			Params struct {
				Message struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		gotMethod = req.Method
		if len(req.Params.Message.Parts) > 0 {
			gotText = req.Params.Message.Parts[0].Text
		}
		// Respond with a completed A2A Task carrying an artifact.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"kind":"task","status":{"state":"completed"},"artifacts":[{"artifactId":"a1","parts":[{"kind":"text","text":"remote replied: hi"}]}]}}`))
	}))
	defer srv.Close()

	h := &A2ACallHandler{HTTPClient: srv.Client()}
	out, err := h.Execute(context.Background(), map[string]interface{}{
		"endpoint":   srv.URL,
		"message":    "hi there",
		"auth_token": "sk-remote",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotMethod != "message/send" {
		t.Errorf("remote saw method %q, want message/send", gotMethod)
	}
	if gotText != "hi there" {
		t.Errorf("remote saw text %q", gotText)
	}
	if gotAuth != "Bearer sk-remote" {
		t.Errorf("remote saw auth %q, want Bearer sk-remote", gotAuth)
	}
	if !strings.Contains(out, "remote replied: hi") {
		t.Errorf("output = %q, want remote artifact text", out)
	}
}

func TestA2ACallFailedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"kind":"task","status":{"state":"failed","message":{"parts":[{"kind":"text","text":"boom"}]}}}}`))
	}))
	defer srv.Close()
	h := &A2ACallHandler{HTTPClient: srv.Client()}
	if _, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi"}); err == nil {
		t.Error("expected error for failed task")
	}
}

func TestA2ACallUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	h := &A2ACallHandler{HTTPClient: srv.Client()}
	if _, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi"}); err == nil {
		t.Error("expected error for 401")
	}
}

func TestA2ACallRpcError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"method not found"}}`))
	}))
	defer srv.Close()
	h := &A2ACallHandler{HTTPClient: srv.Client()}
	if _, err := h.Execute(context.Background(), map[string]interface{}{"endpoint": srv.URL, "message": "hi"}); err == nil {
		t.Error("expected error for rpc error")
	}
}
