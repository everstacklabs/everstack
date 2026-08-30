package telemetry

import (
	"context"
	"testing"
)

func TestWithSession_SetsSessionAndUser(t *testing.T) {
	ctx := WithSession(context.Background(), "sess_1", "user_1")
	tc := GetTraceContext(ctx)
	if tc.SessionID != "sess_1" {
		t.Fatalf("session = %q, want sess_1", tc.SessionID)
	}
	if tc.UserID != "user_1" {
		t.Fatalf("user = %q, want user_1", tc.UserID)
	}
}

func TestWithSession_EmptyArgsNoop(t *testing.T) {
	parent := context.Background()
	got := WithSession(parent, "", "")
	if got != parent {
		t.Fatal("WithSession with empty args should return the parent context unchanged")
	}
}

func TestWithSession_OnlyOverridesNonEmpty(t *testing.T) {
	ctx := WithSession(context.Background(), "sess_1", "user_1")
	// Setting only a user keeps the existing session.
	ctx = WithSession(ctx, "", "user_2")
	tc := GetTraceContext(ctx)
	if tc.SessionID != "sess_1" {
		t.Fatalf("session = %q, want sess_1 preserved", tc.SessionID)
	}
	if tc.UserID != "user_2" {
		t.Fatalf("user = %q, want user_2", tc.UserID)
	}
}

func TestWithSession_DoesNotMutateParent(t *testing.T) {
	parent := WithSession(context.Background(), "sess_parent", "user_parent")
	_ = WithSession(parent, "sess_child", "user_child")
	// The parent's trace context must be unchanged (clone semantics), so a
	// sibling goroutine reading the parent never sees the child's session.
	tc := GetTraceContext(parent)
	if tc.SessionID != "sess_parent" {
		t.Fatalf("parent session mutated to %q, want sess_parent", tc.SessionID)
	}
	if tc.UserID != "user_parent" {
		t.Fatalf("parent user mutated to %q, want user_parent", tc.UserID)
	}
}

func TestResolveSessionUser_PrefersInherited(t *testing.T) {
	ctx := WithSession(context.Background(), "inherited", "u")
	session, user := resolveSessionUser(ctx, "fallback")
	if session != "inherited" {
		t.Fatalf("session = %q, want inherited (inherited must win over fallback)", session)
	}
	if user != "u" {
		t.Fatalf("user = %q, want u", user)
	}
}

func TestResolveSessionUser_FallsBackWhenAbsent(t *testing.T) {
	session, user := resolveSessionUser(context.Background(), "fallback")
	if session != "fallback" {
		t.Fatalf("session = %q, want fallback", session)
	}
	if user != "" {
		t.Fatalf("user = %q, want empty", user)
	}
}
