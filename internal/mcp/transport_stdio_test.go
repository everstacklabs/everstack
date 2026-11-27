package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a test: it is the fake MCP server the stdio tests
// spawn by re-executing the test binary. It only runs when the stdio
// transport launched it with the marker variable in the configured env.
//
// Modes (MCP_STDIO_HELPER_MODE):
//   - echo-env: answer every JSON-RPC request with a result that satisfies
//     initialize and tools/list and also carries the child's os.Environ().
//   - marker:   write the file named by MCP_STDIO_MARKER and exit. Used to
//     prove a refused registration never exec'd anything.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	switch os.Getenv("MCP_STDIO_HELPER_MODE") {
	case "marker":
		_ = os.WriteFile(os.Getenv("MCP_STDIO_MARKER"), []byte("exec happened\n"), 0o644)
		return
	case "echo-env":
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		out := bufio.NewWriter(os.Stdout)
		for scanner.Scan() {
			var req JsonRpcRequest
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.ID == nil {
				continue
			}
			result := map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "stdio-helper", "version": "0"},
				"capabilities":    map[string]interface{}{},
				"tools":           []interface{}{},
				"env":             os.Environ(),
			}
			resp, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
			_, _ = out.Write(append(resp, '\n'))
			_ = out.Flush()
		}
	}
}

// helperStdioConfig returns a StdioConfig that re-executes this test binary
// as the helper above. GO_WANT_HELPER_PROCESS travels in cfg.Env, which is
// also the proof that configured env reaches the child while the parent's
// env does not.
func helperStdioConfig(mode string, extra map[string]string) *StdioConfig {
	env := map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"MCP_STDIO_HELPER_MODE":  mode,
	}
	for k, v := range extra {
		env[k] = v
	}
	return &StdioConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestHelperProcess$", "--"},
		Env:     env,
	}
}

// clearInheritedEnv unsets every allowlisted variable for the duration of
// the test so assertions do not depend on the machine running them.
// t.Setenv registers the restore; os.Unsetenv then removes the value.
func clearInheritedEnv(t *testing.T) {
	t.Helper()
	for _, k := range stdioInheritedEnv {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestChildEnv_AllowlistPlusConfiguredOnly(t *testing.T) {
	clearInheritedEnv(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/var/lib/gateway")
	t.Setenv("EVS_M2M_SIGNING_KEY", "gateway-secret")
	t.Setenv("EVS_PLATFORM_DSN", "postgres://platform")

	got := childEnv(StdioConfig{Env: map[string]string{"B_VAR": "2", "A_VAR": "1"}})
	want := []string{"PATH=/usr/bin:/bin", "HOME=/var/lib/gateway", "A_VAR=1", "B_VAR=2"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("childEnv = %q, want %q", got, want)
	}

	// Configured env may override an inherited var without duplicating it.
	got = childEnv(StdioConfig{Env: map[string]string{"HOME": "/tmp/mcp"}})
	want = []string{"PATH=/usr/bin:/bin", "HOME=/tmp/mcp"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("childEnv with override = %q, want %q", got, want)
	}
}

// TestChildEnv_PassesSelfHostedEssentials pins the passthrough list that
// common self-hosted setups depend on (egress proxy, private CA, temp dir,
// locale, timezone, user), in allowlist order, while secret-shaped and
// gateway-namespaced variables still stay out.
func TestChildEnv_PassesSelfHostedEssentials(t *testing.T) {
	clearInheritedEnv(t)
	essentials := []struct{ k, v string }{
		{"PATH", "/usr/bin"},
		{"HOME", "/home/gw"},
		{"USER", "gw"},
		{"TMPDIR", "/var/tmp"},
		{"LANG", "en_US.UTF-8"},
		{"LC_ALL", "C.UTF-8"},
		{"TZ", "Europe/Paris"},
		{"HTTP_PROXY", "http://proxy:3128"},
		{"HTTPS_PROXY", "http://proxy:3128"},
		{"NO_PROXY", "localhost,.internal"},
		{"http_proxy", "http://proxy:3128"},
		{"https_proxy", "http://proxy:3128"},
		{"no_proxy", "localhost"},
		{"SSL_CERT_FILE", "/etc/ssl/corp.pem"},
		{"SSL_CERT_DIR", "/etc/ssl/certs"},
		{"NODE_EXTRA_CA_CERTS", "/etc/ssl/corp.pem"},
	}
	for _, e := range essentials {
		t.Setenv(e.k, e.v)
	}
	for _, k := range []string{
		"EVS_M2M_SIGNING_KEY", "EVS_PLATFORM_DSN", "MF_DATABASE_POSTGRES_DSN",
		"OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "DATABASE_URL",
		"PGPASSWORD", "STRIPE_SECRET_KEY",
	} {
		t.Setenv(k, "secret")
	}

	got := childEnv(StdioConfig{})
	want := make([]string, 0, len(essentials))
	for _, e := range essentials {
		want = append(want, e.k+"="+e.v)
	}
	// Env names are case-sensitive on darwin and linux, so both proxy
	// spellings are distinct entries and both are passed.
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("childEnv = %q\nwant %q", got, want)
	}

	// The allowlist itself must never grow a secret-shaped or gateway name.
	for _, k := range stdioInheritedEnv {
		upper := strings.ToUpper(k)
		if strings.HasPrefix(upper, "EVS_") || strings.HasPrefix(upper, "MF_") {
			t.Errorf("gateway-namespaced variable in stdio allowlist: %s", k)
		}
		for _, bad := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASS", "DSN", "CREDENTIAL", "AUTH"} {
			if strings.Contains(upper, bad) {
				t.Errorf("secret-shaped variable in stdio allowlist: %s", k)
			}
		}
	}
}

// TestStdioTransport_ChildDoesNotInheritGatewayEnv drives a real child
// process and reads back its environment. It also pins the numeric id
// round trip: the request id is an int64 and the echoed id decodes as a
// float64, so this Send would time out if the pending map were keyed on
// the raw interface{} value.
func TestStdioTransport_ChildDoesNotInheritGatewayEnv(t *testing.T) {
	t.Setenv("EVS_M2M_SIGNING_KEY", "gateway-secret")
	t.Setenv("EVS_PLATFORM_DSN", "postgres://platform")
	t.Setenv("OPENAI_API_KEY", "provider-secret")
	t.Setenv("TZ", "Europe/Paris")

	cfg := ServerConfig{
		ID:            "env-probe",
		TenantID:      "tenant-a",
		Name:          "env-probe",
		TransportType: TransportStdio,
		StdioConfig:   helperStdioConfig("echo-env", map[string]string{"CUSTOM_VAR": "hello"}),
	}
	tp, err := newStdioTransport(cfg)
	if err != nil {
		t.Fatalf("newStdioTransport: %v", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := tp.Send(ctx, &JsonRpcRequest{Method: "env"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var result struct {
		Env []string `json:"env"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Env) == 0 {
		t.Fatal("child reported an empty environment")
	}

	var sawCustom, sawPath, sawTZ bool
	for _, kv := range result.Env {
		switch {
		case strings.HasPrefix(kv, "EVS_"), strings.HasPrefix(kv, "OPENAI_API_KEY="):
			t.Errorf("gateway variable leaked into stdio child: %s", kv)
		case kv == "TZ=Europe/Paris":
			sawTZ = true
		case kv == "CUSTOM_VAR=hello":
			sawCustom = true
		case strings.HasPrefix(kv, "PATH="):
			sawPath = true
		}
	}
	if !sawCustom {
		t.Errorf("configured env did not reach the child: %q", result.Env)
	}
	if !sawPath {
		t.Errorf("PATH was not inherited by the child: %q", result.Env)
	}
	if !sawTZ {
		t.Errorf("allowlisted TZ was not inherited by the child: %q", result.Env)
	}
}

func TestRegistry_RefusesStdioWhenDisallowed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "exec-marker")
	t.Cleanup(SetStdioPolicyForTest(true))
	reg := NewRegistry(nil)
	reg.DisallowStdio()
	if reg.StdioAllowed() {
		t.Fatal("StdioAllowed should be false after DisallowStdio")
	}

	cfg := ServerConfig{
		ID:            "pre-existing-stdio-row",
		TenantID:      "tenant-a",
		Name:          "tenant stdio server",
		TransportType: TransportStdio,
		StdioConfig:   helperStdioConfig("marker", map[string]string{"MCP_STDIO_MARKER": marker}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entry, err := reg.Register(ctx, cfg)
	if !errors.Is(err, ErrStdioNotAllowed) {
		t.Fatalf("Register error = %v, want ErrStdioNotAllowed", err)
	}
	if entry != nil {
		t.Fatalf("Register returned an entry despite refusal: %+v", entry)
	}
	if _, ok := reg.Get(cfg.ID); ok {
		t.Fatal("refused server must not appear in the registry")
	}

	// Give a would-be child a moment, then prove nothing ran.
	time.Sleep(200 * time.Millisecond)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("marker exists (stat err=%v): the configured command was executed", statErr)
	}

	// Non-stdio transports are unaffected by the switch; the failure here
	// must be a connection error, not the policy error.
	_, err = reg.Register(ctx, ServerConfig{
		ID: "sse", TenantID: "tenant-a", Name: "sse", TransportType: TransportSSE,
		URL: "http://127.0.0.1:1/unreachable",
	})
	if err == nil || errors.Is(err, ErrStdioNotAllowed) {
		t.Fatalf("SSE registration should fail on connection, not policy: %v", err)
	}
}

// TestStdioTransportConstructors_RefuseWhenDisallowed is the defense in
// depth below the registry: a caller that builds a client or transport
// directly (no Registry.Register) is refused by the constructor, and a
// transport built before the switch flipped is refused at exec time. In no
// case does the configured command run.
func TestStdioTransportConstructors_RefuseWhenDisallowed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "exec-marker")
	cfg := ServerConfig{
		ID:            "direct-stdio",
		TenantID:      "tenant-a",
		Name:          "direct stdio",
		TransportType: TransportStdio,
		StdioConfig:   helperStdioConfig("marker", map[string]string{"MCP_STDIO_MARKER": marker}),
	}

	t.Cleanup(SetStdioPolicyForTest(true))
	// Built while stdio is still allowed, started after it is disallowed.
	early, err := newStdioTransport(cfg)
	if err != nil {
		t.Fatalf("newStdioTransport: %v", err)
	}
	t.Cleanup(func() { _ = early.Close() })

	DisallowStdio()

	if tp, err := NewTransport(cfg); !errors.Is(err, ErrStdioNotAllowed) || tp != nil {
		t.Fatalf("NewTransport = (%v, %v), want (nil, ErrStdioNotAllowed)", tp, err)
	}
	if c, err := NewClient(cfg, nil); !errors.Is(err, ErrStdioNotAllowed) || c != nil {
		t.Fatalf("NewClient = (%v, %v), want (nil, ErrStdioNotAllowed)", c, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := early.Send(ctx, &JsonRpcRequest{Method: "initialize"}); !errors.Is(err, ErrStdioNotAllowed) {
		t.Fatalf("Send on a pre-built transport = %v, want ErrStdioNotAllowed", err)
	}

	time.Sleep(200 * time.Millisecond)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("marker exists (stat err=%v): the configured command was executed", statErr)
	}

	// Non-stdio transports are unaffected by the switch.
	if _, err := NewTransport(ServerConfig{Name: "sse", TransportType: TransportSSE, URL: "http://127.0.0.1:1/sse"}); errors.Is(err, ErrStdioNotAllowed) {
		t.Fatalf("SSE transport refused by the stdio policy: %v", err)
	}
}

func TestRegistry_StdioWorksWhenAllowed(t *testing.T) {
	reg := NewRegistry(nil)
	if !reg.StdioAllowed() {
		t.Fatal("fresh registry should allow stdio")
	}
	cfg := ServerConfig{
		ID:            "self-hosted-stdio",
		TenantID:      "local",
		Name:          "self-hosted stdio server",
		TransportType: TransportStdio,
		StdioConfig:   helperStdioConfig("echo-env", nil),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entry, err := reg.Register(ctx, cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { reg.Unregister(cfg.ID) })
	if entry == nil || entry.Client == nil {
		t.Fatalf("Register returned no live entry: %+v", entry)
	}
	if got, ok := reg.Get(cfg.ID); !ok || got != entry {
		t.Fatal("registered stdio server not retrievable from the registry")
	}
	if info := entry.Client.ServerInfo(); info == nil || info.ServerInfo.Name != "stdio-helper" {
		t.Fatalf("initialize handshake did not complete: %+v", info)
	}
	if err := entry.Client.Ping(ctx); err != nil {
		t.Fatalf("Ping over stdio: %v", err)
	}
	fmt.Fprintln(os.Stderr, "stdio helper round trip ok")
}
