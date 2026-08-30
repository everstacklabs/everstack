package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox/toolbox"
)

type httpToolboxResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func TestHTTPToolboxFilesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hello.txt")

	writeReq := toolbox.WriteFileRequest{
		Path:    path,
		Content: base64.StdEncoding.EncodeToString([]byte("hello")),
	}
	writeResp := postHTTPToolbox(t, toolbox.MethodWriteFile, writeReq)
	if writeResp.Error != "" {
		t.Fatalf("write error: %s", writeResp.Error)
	}

	readResp := postHTTPToolbox(t, toolbox.MethodReadFile, toolbox.ReadFileRequest{Path: path})
	if readResp.Error != "" {
		t.Fatalf("read error: %s", readResp.Error)
	}
	var readResult toolbox.ReadFileResponse
	if err := json.Unmarshal(readResp.Result, &readResult); err != nil {
		t.Fatalf("decode read result: %v", err)
	}
	content, err := base64.StdEncoding.DecodeString(readResult.Content)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if string(content) != "hello" || readResult.Size != 5 {
		t.Fatalf("unexpected read result: content=%q size=%d", content, readResult.Size)
	}

	listResp := postHTTPToolbox(t, toolbox.MethodListFiles, toolbox.ListFilesRequest{Path: filepath.Dir(path)})
	if listResp.Error != "" {
		t.Fatalf("list error: %s", listResp.Error)
	}
	var listResult toolbox.ListFilesResponse
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	if len(listResult.Files) != 1 || listResult.Files[0].Name != "hello.txt" || listResult.Files[0].IsDir {
		t.Fatalf("unexpected list result: %+v", listResult.Files)
	}
}

func TestHTTPToolboxExec(t *testing.T) {
	resp := postHTTPToolbox(t, toolbox.MethodExec, toolbox.ExecRequest{
		ID:        "exec-test",
		Command:   []string{"sh", "-c", "printf ok"},
		TimeoutMS: 1000,
	})
	if resp.Error != "" {
		t.Fatalf("exec error: %s", resp.Error)
	}
	var result toolbox.ExecResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode exec result: %v", err)
	}
	if result.ID != "exec-test" || result.ExitCode != 0 || result.Stdout != "ok" || result.TimedOut {
		t.Fatalf("unexpected exec result: %+v", result)
	}
}

func TestHTTPToolboxRejectsNonPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/toolbox/exec", nil)
	rec := httptest.NewRecorder()
	handleHTTPToolboxRPC(toolbox.MethodExec)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func postHTTPToolbox(t *testing.T, method string, body any) httpToolboxResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/toolbox", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	handleHTTPToolboxRPC(method)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp httpToolboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
