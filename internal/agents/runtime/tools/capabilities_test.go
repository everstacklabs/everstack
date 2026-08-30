package tools

import "testing"

func TestToolCapabilityForName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tool string
		want ToolCapability
	}{
		{name: "filesystem", tool: "sandbox_write_file", want: ToolCapabilityFilesystem},
		{name: "process", tool: "sandbox_shell", want: ToolCapabilityProcess},
		{name: "network", tool: "sandbox_expose_port", want: ToolCapabilityNetwork},
		{name: "browser prefix", tool: "browser_click", want: ToolCapabilityBrowser},
		{name: "mcp prefix", tool: "mcp__github__create_issue", want: ToolCapabilityMCP},
		{name: "human", tool: "ask_user", want: ToolCapabilityHumanInteraction},
		{name: "platform mutation", tool: "platform_delete_agent", want: ToolCapabilityPlatformMutation},
		{name: "unknown sandbox", tool: "sandbox_future_tool", want: ToolCapabilitySandboxRuntime},
		{name: "unknown", tool: "custom_tool", want: ToolCapabilityUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolCapabilityForName(tc.tool); got != tc.want {
				t.Fatalf("ToolCapabilityForName(%q) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}
