package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// decodeErrorResp decodes the standard JSON error envelope from a response.
func decodeErrorResp(t *testing.T, w *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	return errObj["code"].(string), errObj["message"].(string)
}

func TestHandleListSandboxFiles_SandboxNotEnabled(t *testing.T) {
	s := &Server{} // sandboxMgr is nil
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/sandbox/sess123/files?path=/repo", nil)
	s.handleListSandboxFiles(w, r, map[string]string{"session_id": "sess123"})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	code, _ := decodeErrorResp(t, w)
	if code != "unavailable" {
		t.Fatalf("expected code unavailable, got %s", code)
	}
}

// TestPathValidationLogic tests the path validation logic that the handler uses.
// Since we can't easily mock SandboxManager (it panics with nil backend), we test
// the path validation rules directly.
func TestPathValidationLogic(t *testing.T) {
	allowedRoots := []string{"/repo", "/workspace"}

	isAllowed := func(path string) bool {
		path = filepath.Clean(path)
		if !strings.HasPrefix(path, "/") {
			return false
		}
		for _, root := range allowedRoots {
			if path == root || strings.HasPrefix(path, root+"/") {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name    string
		path    string
		allowed bool
	}{
		{"repo root", "/repo", true},
		{"repo subpath", "/repo/src/main.go", true},
		{"trooper root", "/workspace", true},
		{"trooper subpath", "/workspace/project/lib", true},
		{"etc", "/etc", false},
		{"root", "/", false},
		{"home", "/home/user", false},
		{"path traversal from repo", "/repo/../../etc/passwd", false},
		{"path traversal from trooper", "/workspace/../etc/passwd", false},
		{"relative path", "repo/src", false},
		{"repos with extra text", "/repository", false},
		{"trooper with extra text", "/workspaces", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowed(tt.path)
			if got != tt.allowed {
				t.Fatalf("isAllowed(%q) = %v, want %v", tt.path, got, tt.allowed)
			}
		})
	}
}

// TestHandleListSandboxFiles_EmptySessionID verifies the handler with nil sandboxMgr
// returns 503 before even checking session_id.
func TestHandleListSandboxFiles_EmptySessionID(t *testing.T) {
	s := &Server{} // sandboxMgr is nil
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/sandbox//files?path=/repo", nil)
	s.handleListSandboxFiles(w, r, map[string]string{"session_id": ""})

	// sandboxMgr nil check comes first
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (sandbox not enabled takes precedence), got %d", w.Code)
	}
}

// TestHandleListSandboxFiles_ResponseShape verifies the JSON response structure
// by checking that a sandbox-not-enabled response has the correct shape.
func TestHandleListSandboxFiles_ResponseShape(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/sandbox/sess123/files?path=/repo", nil)
	s.handleListSandboxFiles(w, r, map[string]string{"session_id": "sess123"})

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify error response has correct envelope
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object")
	}
	if _, ok := errObj["code"]; !ok {
		t.Fatal("error should have 'code' field")
	}
	if _, ok := errObj["message"]; !ok {
		t.Fatal("error should have 'message' field")
	}
}
