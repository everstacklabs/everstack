//go:build ui_embed

package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestEmbeddedSPAHandlerInjectsRuntimeEnvIntoExactRootIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://prod-3fa6c9.dev.eu-gra-1.everstack.ai/", nil)
	req = req.WithContext(contextkeys.WithRequestInstanceScope(req.Context(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1", OrganizationSlug: "everstack",
	}))
	recorder := httptest.NewRecorder()
	NewSPAHandler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "window.__env=Object.assign") || !strings.Contains(body, `"ORGANIZATION_SLUG":"everstack"`) {
		t.Fatalf("embedded root index did not receive runtime environment: %s", body)
	}
	if got, want := recorder.Header().Get("Content-Length"), fmt.Sprintf("%d", len(body)); got != want {
		t.Fatalf("Content-Length = %q, want %q", got, want)
	}
}
