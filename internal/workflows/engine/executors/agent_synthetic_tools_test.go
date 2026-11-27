package executors

import (
	"context"
	"testing"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestAttachSyntheticToolsKeepsReservedStoredAgentSandboxName(t *testing.T) {
	loopInput := &agentrt.LoopInput{}
	agent := &agentConfig{
		ID: "agent-1",
		RuntimeConfig: map[string]interface{}{
			"sandbox": map[string]interface{}{
				"enabled": true,
				"name":    "sbx_stored",
			},
		},
	}
	executor := &AgentExecutor{SandboxManager: &sandbox.SandboxManager{}}
	executor.attachSyntheticTools(context.Background(), agent, loopInput, "session-1", "tenant-a", nil, time.Now())
	if loopInput.Interceptor == nil {
		t.Fatal("sandbox tools were not attached")
	}
	found := false
	for _, tool := range loopInput.Tools {
		if tool == "sandbox_shell" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tools = %#v, want sandbox_shell", loopInput.Tools)
	}
}
