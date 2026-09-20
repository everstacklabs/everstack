package openaiapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	executorpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
)

// Docker must fail before a live test opens a database, reads either process
// credential, creates an upstream session, or provisions a sandbox. The
// current Docker DNS sidecar is not a hard outbound boundary, so it cannot
// qualify an OpenAI self_hosted environment.
func TestLiveOpenAIAgentDockerSandboxFailsConfigurationPreflight(t *testing.T) {
	adapter := ExecutorAdapter{
		Service:     executorpkg.New(nil),
		BackendName: "docker",
	}
	err := adapter.ValidateConfiguration(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Docker preflight error = %v, want unavailable", err)
	}
	if !strings.Contains(err.Error(), `sandbox backend "docker" is unsupported`) {
		t.Fatalf("Docker preflight did not explain unsupported backend: %v", err)
	}
}
