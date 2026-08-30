package tools

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestNewSandboxHandlers_HidesGitCloneWhenRepoPreconfigured(t *testing.T) {
	t.Parallel()

	ctx := &SandboxSessionContext{
		Config: sandbox.SandboxConfig{
			GitRepoURL: "everstacklabs/model-catalog",
		},
	}

	handlers := NewSandboxHandlers(ctx)
	for _, h := range handlers {
		if h.Name() == "sandbox_git_clone" {
			t.Fatalf("expected sandbox_git_clone to be hidden when git_repo_url is preconfigured")
		}
	}
}

func TestNewSandboxHandlers_ShowsGitCloneWhenRepoNotPreconfigured(t *testing.T) {
	t.Parallel()

	ctx := &SandboxSessionContext{
		Config: sandbox.SandboxConfig{},
	}

	handlers := NewSandboxHandlers(ctx)
	found := false
	for _, h := range handlers {
		if h.Name() == "sandbox_git_clone" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sandbox_git_clone to be available when git_repo_url is not preconfigured")
	}
}

