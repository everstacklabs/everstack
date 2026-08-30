package v1

// MCP setup helpers (POR-95).
//
// GET /v1/mcp/config?client=claude -- returns the JSON config block to add to
//   ~/.claude/claude_desktop_config.json (or Cursor/Windsurf equivalents).
// This mirrors `everstack mcp init claude` output so the frontend can show
// a copy-paste config without requiring the CLI.

import (
	"encoding/json"
	"net/http"
)

// HandleMCPConfig returns the MCP server configuration for a given client.
// GET /v1/mcp/config?client=claude|cursor|windsurf
func (s *Server) HandleMCPConfig(w http.ResponseWriter, r *http.Request) {
	client := r.URL.Query().Get("client")
	if client == "" {
		client = "claude"
	}

	// Resolve the CLI binary name and the config file path per client.
	type clientConfig struct {
		BinaryName string
		ConfigFile string
		ConfigKey  string
	}
	clients := map[string]clientConfig{
		"claude":   {BinaryName: "everstack", ConfigFile: "~/.claude/claude_desktop_config.json", ConfigKey: "mcpServers"},
		"cursor":   {BinaryName: "everstack", ConfigFile: "~/.cursor/mcp.json", ConfigKey: "mcpServers"},
		"windsurf": {BinaryName: "everstack", ConfigFile: "~/.codeium/windsurf/mcp_config.json", ConfigKey: "mcpServers"},
	}
	cc, ok := clients[client]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown client; supported: claude, cursor, windsurf")
		return
	}

	// The MCP server config block.
	mcpBlock := map[string]interface{}{
		"everstack": map[string]interface{}{
			"command": cc.BinaryName,
			"args":    []string{"mcp", "start"},
			"env":     map[string]string{},
		},
	}

	// The full config object (to merge into the client's settings file).
	configBlock := map[string]interface{}{
		cc.ConfigKey: mcpBlock,
	}
	configJSON, _ := json.MarshalIndent(configBlock, "", "  ")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"client":       client,
		"config_file":  cc.ConfigFile,
		"config_block": string(configJSON),
		"instructions": []string{
			// get.everstack.ai/ only 302s to the docs page with an empty body, so
			// the bare host piped into a shell was a no-op. The script lives at
			// /install.sh and is bash, not sh.
			"1. Install the Everstack CLI: curl -fsSL https://get.everstack.ai/install.sh | bash",
			"2. Login: evs login",
			"3. Add the config block to " + cc.ConfigFile,
			"4. Restart " + client,
			"5. The 'Everstack' MCP server will appear in your tools list",
		},
		"tools": []string{
			"sandbox_create", "sandbox_exec", "sandbox_shell", "sandbox_list_files",
			"sandbox_lsp_diagnostics", "sandbox_lsp_symbols", "sandbox_screenshot",
			"sandbox_expose_port",
		},
	})
}
