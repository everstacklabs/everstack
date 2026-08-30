package main

// Language Server Protocol (LSP) manager for the sandbox-agent.
//
// Starts language servers (pylsp, typescript-language-server) on demand,
// manages their lifecycle, and translates HTTP requests into LSP JSON-RPC.
//
// Protocol: LSP uses JSON-RPC 2.0 over stdio with HTTP-style framing:
//   Content-Length: N\r\n\r\n<json>
//
// One server instance per language per sandbox-agent process.
// Servers are started on the first request and kept alive until the
// sandbox-agent exits.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// lspManager manages one language server process.
type lspManager struct {
	lang    string // "python" or "typescript"
	cmd     string // server binary
	cmdArgs []string

	mu      sync.Mutex
	proc    *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	nextID  atomic.Int64
	started bool
}

var (
	pythonLSP     = &lspManager{lang: "python", cmd: "pylsp"}
	typescriptLSP = &lspManager{
		lang:    "typescript",
		cmd:     "typescript-language-server",
		cmdArgs: []string{"--stdio"},
	}
)

// start launches the language server if not already running.
func (m *lspManager) start(projectPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}

	// Resolve the binary.
	bin, err := exec.LookPath(m.cmd)
	if err != nil {
		// Try fallback alternatives.
		for _, alt := range lspAlternatives(m.lang) {
			if b, e := exec.LookPath(alt); e == nil {
				bin = b
				break
			}
		}
		if bin == "" {
			return fmt.Errorf("lsp %s: %s not found (install it in the sandbox first)", m.lang, m.cmd)
		}
	}

	args := m.cmdArgs
	cmd := exec.Command(bin, args...)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("lsp %s: stdin pipe: %w", m.lang, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("lsp %s: stdout pipe: %w", m.lang, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("lsp %s: start: %w", m.lang, err)
	}

	m.proc = cmd
	m.stdin = stdin
	m.stdout = bufio.NewReader(stdout)
	m.started = true

	// Send initialize request.
	initParams := map[string]interface{}{
		"processId":    os.Getpid(),
		"rootUri":      dirToURI(projectPath),
		"capabilities": map[string]interface{}{},
	}
	if _, err := m.call("initialize", initParams); err != nil {
		_ = m.stop()
		return fmt.Errorf("lsp %s: initialize: %w", m.lang, err)
	}
	// Send initialized notification.
	_ = m.notify("initialized", map[string]interface{}{})
	return nil
}

func (m *lspManager) stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return nil
	}
	m.started = false
	if m.proc != nil && m.proc.Process != nil {
		_ = m.proc.Process.Signal(os.Interrupt)
	}
	if m.stdin != nil {
		_ = m.stdin.Close()
	}
	return nil
}

// call sends a request and reads the response.
func (m *lspManager) call(method string, params interface{}) (json.RawMessage, error) {
	id := m.nextID.Add(1)
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	if _, err := fmt.Fprint(m.stdin, msg); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	return m.readResponse(id)
}

// notify sends a notification (no response expected).
func (m *lspManager) notify(method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	body, _ := json.Marshal(req)
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	_, err := fmt.Fprint(m.stdin, msg)
	return err
}

// readResponse reads and parses one LSP response. Skips server-to-client
// notifications (requests without an "id" matching ours).
func (m *lspManager) readResponse(id int64) (json.RawMessage, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// Read headers.
		length := 0
		for {
			line, err := m.stdout.ReadString('\n')
			if err != nil {
				return nil, fmt.Errorf("read header: %w", err)
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
				length = n
			}
		}
		if length == 0 {
			continue
		}
		// Read body.
		buf := make([]byte, length)
		if _, err := io.ReadFull(m.stdout, buf); err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		var resp struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(buf, &resp); err != nil {
			continue // skip malformed frames
		}
		if resp.ID == nil || *resp.ID != id {
			continue // server notification or different response, skip
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("lsp error: %s", resp.Error.Message)
		}
		return resp.Result, nil
	}
	return nil, fmt.Errorf("timeout waiting for LSP response")
}

func (m *lspManager) didOpen(path, text, languageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        fileToURI(path),
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

func (m *lspManager) didClose(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notify("textDocument/didClose", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": fileToURI(path)},
	})
}

func (m *lspManager) completions(path string, line, char int) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.call("textDocument/completion", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": fileToURI(path)},
		"position":     map[string]int{"line": line, "character": char},
	})
}

func (m *lspManager) documentSymbols(path string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.call("textDocument/documentSymbol", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": fileToURI(path)},
	})
}

func (m *lspManager) definition(path string, line, char int) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.call("textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": fileToURI(path)},
		"position":     map[string]int{"line": line, "character": char},
	})
}

func (m *lspManager) hover(path string, line, char int) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.call("textDocument/hover", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": fileToURI(path)},
		"position":     map[string]int{"line": line, "character": char},
	})
}

func dirToURI(path string) string {
	if path == "" {
		return "file:///workspace"
	}
	return "file://" + path
}

func fileToURI(path string) string {
	if strings.HasPrefix(path, "file://") {
		return path
	}
	return "file://" + path
}

func lspAlternatives(lang string) []string {
	switch lang {
	case "python":
		return []string{"python-language-server", "pyright-langserver", "jedi-language-server"}
	case "typescript":
		return []string{"tsserver"}
	}
	return nil
}

// ─── HTTP Handlers ──────────────────────────────────────────────────

// handleLSP routes /lsp/* requests to the correct language server.
func handleLSP(w http.ResponseWriter, r *http.Request) {
	// Path: /lsp/{lang}/{operation}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/lsp/"), "/", 2)
	if len(parts) < 2 {
		http.Error(w, `{"error":"invalid lsp path"}`, http.StatusBadRequest)
		return
	}
	lang, op := parts[0], parts[1]

	var mgr *lspManager
	var languageID string
	switch lang {
	case "python":
		mgr = pythonLSP
		languageID = "python"
	case "typescript", "javascript":
		mgr = typescriptLSP
		languageID = "typescript"
	default:
		http.Error(w, `{"error":"unsupported language"}`, http.StatusBadRequest)
		return
	}

	switch op {
	case "start":
		var body struct {
			ProjectPath string `json:"project_path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := mgr.start(body.ProjectPath); err != nil {
			lspError(w, err.Error())
			return
		}
		lspJSON(w, map[string]string{"status": "started", "language": lang})

	case "stop":
		_ = mgr.stop()
		lspJSON(w, map[string]string{"status": "stopped"})

	case "did-open":
		var body struct {
			Path string `json:"path"`
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := mgr.didOpen(body.Path, body.Text, languageID); err != nil {
			lspError(w, err.Error())
			return
		}
		lspJSON(w, map[string]string{"status": "ok"})

	case "did-close":
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = mgr.didClose(body.Path)
		lspJSON(w, map[string]string{"status": "ok"})

	case "completions":
		path := r.URL.Query().Get("path")
		line, _ := strconv.Atoi(r.URL.Query().Get("line"))
		char, _ := strconv.Atoi(r.URL.Query().Get("character"))
		result, err := mgr.completions(path, line, char)
		if err != nil {
			lspError(w, err.Error())
			return
		}
		lspRaw(w, result)

	case "document-symbols":
		path := r.URL.Query().Get("path")
		result, err := mgr.documentSymbols(path)
		if err != nil {
			lspError(w, err.Error())
			return
		}
		lspRaw(w, result)

	case "definition":
		path := r.URL.Query().Get("path")
		line, _ := strconv.Atoi(r.URL.Query().Get("line"))
		char, _ := strconv.Atoi(r.URL.Query().Get("character"))
		result, err := mgr.definition(path, line, char)
		if err != nil {
			lspError(w, err.Error())
			return
		}
		lspRaw(w, result)

	case "hover":
		path := r.URL.Query().Get("path")
		line, _ := strconv.Atoi(r.URL.Query().Get("line"))
		char, _ := strconv.Atoi(r.URL.Query().Get("character"))
		result, err := mgr.hover(path, line, char)
		if err != nil {
			lspError(w, err.Error())
			return
		}
		lspRaw(w, result)

	default:
		http.Error(w, `{"error":"unknown lsp operation"}`, http.StatusBadRequest)
	}
}

func lspJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func lspRaw(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		_, _ = w.Write([]byte("[]"))
		return
	}
	_, _ = w.Write(raw)
}

func lspError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
