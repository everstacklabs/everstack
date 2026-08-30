package sandbox

import "testing"

func TestLazyGitCloneEnabledForSession(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{
		lazyGitClone:    true,
		lazyGitClonePct: 100,
	}

	if !m.lazyGitCloneEnabledForSession("session-a") {
		t.Fatalf("expected lazy clone to be enabled for 100%% rollout")
	}

	m.lazyGitClonePct = 0
	if m.lazyGitCloneEnabledForSession("session-a") {
		t.Fatalf("expected lazy clone to be disabled for 0%% rollout")
	}

	m.lazyGitClone = false
	m.lazyGitClonePct = 100
	if m.lazyGitCloneEnabledForSession("session-a") {
		t.Fatalf("expected lazy clone to be disabled when feature flag is off")
	}
}

func TestLazyGitCloneDeterministicBucket(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{
		lazyGitClone:    true,
		lazyGitClonePct: 35,
	}

	a := m.lazyGitCloneEnabledForSession("session-deterministic")
	b := m.lazyGitCloneEnabledForSession("session-deterministic")
	if a != b {
		t.Fatalf("expected deterministic rollout decision, got %v then %v", a, b)
	}
}
