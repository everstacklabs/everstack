//go:build !ui_embed

package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestSPAHandlerInjectsRuntimeEnvIntoExactRootIndex(t *testing.T) {
	t.Setenv("EVS_UI_PROXY_URL", "")
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html><head></head><body>app</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldRoot := UIRootOverride
	UIRootOverride = dist
	t.Cleanup(func() { UIRootOverride = oldRoot })

	req := requestWithOrganizationScope(t)
	recorder := httptest.NewRecorder()
	NewSPAHandler().ServeHTTP(recorder, req)
	assertRuntimeEnvRootResponse(t, recorder)
}

func requestWithOrganizationScope(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://prod-3fa6c9.dev.eu-gra-1.everstack.ai/", nil)
	return req.WithContext(contextkeys.WithRequestInstanceScope(req.Context(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1", OrganizationSlug: "everstack",
	}))
}

func assertRuntimeEnvRootResponse(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "window.__env=Object.assign") || !strings.Contains(body, `"ORGANIZATION_SLUG":"everstack"`) {
		t.Fatalf("exact root index did not receive runtime environment: %s", body)
	}
	if got, want := recorder.Header().Get("Content-Length"), fmt.Sprintf("%d", len(body)); got != want {
		t.Fatalf("Content-Length = %q, want %q", got, want)
	}
}
