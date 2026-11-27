package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// gatewayModels is what the native /v1/gateway/models handler returns.
const gatewayModels = `{"providers":[{"provider":"openai","models":["gpt-5.5","gpt-5.2-codex"]}]}`

// /v1 is the documented OpenAI-compatible base URL, so an SDK calling
// client.models.list() requests /v1/models. The grpc-gateway has no route for
// it and answered 404 even for an authenticated caller, which left model
// discovery broken on the very base URL the docs hand out.
func TestV1ModelsAliasServesOpenAIListing(t *testing.T) {
	var innerPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(gatewayModels))
	})

	rec := httptest.NewRecorder()
	withV1OpenAICompat(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if innerPath != "/v1/gateway/models" {
		t.Fatalf("inner handler saw %q, want the native models route", innerPath)
	}

	var parsed struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if parsed.Object != "list" {
		t.Fatalf("object = %q, want list (OpenAI listing shape)", parsed.Object)
	}
	if len(parsed.Data) != 2 || parsed.Data[0].Object != "model" {
		t.Fatalf("unexpected listing: %+v", parsed.Data)
	}
}

// The alias is a narrow shim on a broad prefix: everything else on /v1 has to
// reach the gateway untouched, at its original path.
func TestV1ModelsAliasPassesEverythingElseThrough(t *testing.T) {
	for name, req := range map[string]*http.Request{
		"chat completions": httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
		"models non-GET":   httptest.NewRequest(http.MethodPost, "/v1/models", nil),
		"native models":    httptest.NewRequest(http.MethodGet, "/v1/gateway/models", nil),
		"otlp traces":      httptest.NewRequest(http.MethodPost, "/v1/traces", nil),
		"models sub-path":  httptest.NewRequest(http.MethodGet, "/v1/models/gpt-5.5", nil),
	} {
		passed := false
		gotPath := ""
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed, gotPath = true, r.URL.Path
		})

		withV1OpenAICompat(inner).ServeHTTP(httptest.NewRecorder(), req)

		if !passed {
			t.Fatalf("%s was intercepted by the alias", name)
		}
		if gotPath != req.URL.Path {
			t.Fatalf("%s path rewritten to %q, want %q", name, gotPath, req.URL.Path)
		}
	}
}

// A failure from the inner handler must surface as an OpenAI error envelope
// rather than a raw proto error, since an SDK is parsing this response.
func TestV1ModelsAliasConvertsErrors(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":13,"message":"boom"}`))
	})

	rec := httptest.NewRecorder()
	withV1OpenAICompat(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if rec.Code == http.StatusOK {
		t.Fatal("inner failure was reported as success")
	}
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("error body not JSON: %v", err)
	}
	if parsed.Error == nil {
		t.Fatalf("not an OpenAI error envelope: %s", rec.Body.String())
	}
}
