package scoringstate

import "testing"

func TestIdempotencyKeyStableAndDistinct(t *testing.T) {
	k1 := IdempotencyKey("task_completion", "trace-1", 3)
	k2 := IdempotencyKey("task_completion", "trace-1", 3)
	if k1 != k2 {
		t.Errorf("same inputs gave different keys: %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("key length = %d, want 64 (sha256 hex)", len(k1))
	}
	// Any input change must change the key.
	if IdempotencyKey("policy", "trace-1", 3) == k1 {
		t.Error("different scorer produced same key")
	}
	if IdempotencyKey("task_completion", "trace-2", 3) == k1 {
		t.Error("different trace produced same key")
	}
	if IdempotencyKey("task_completion", "trace-1", 4) == k1 {
		t.Error("different discriminator produced same key")
	}
}

func TestStateLifecycle(t *testing.T) {
	s := NewState("trace-1", "turn")

	tf := s.Trigger("task_completion", 3)
	if tf.Status != StatusPending || tf.Attempts != 1 {
		t.Fatalf("after trigger: status=%s attempts=%d", tf.Status, tf.Attempts)
	}
	if tf.IdempotencyKey != IdempotencyKey("task_completion", "trace-1", 3) {
		t.Errorf("idempotency key not set correctly")
	}

	// Re-trigger keeps the same key and bumps attempts.
	tf2 := s.Trigger("task_completion", 3)
	if tf2.Attempts != 2 || tf2.IdempotencyKey != tf.IdempotencyKey {
		t.Errorf("retrigger: attempts=%d key-stable=%v", tf2.Attempts, tf2.IdempotencyKey == tf.IdempotencyKey)
	}

	s.Trigger("policy", 3)
	s.Trigger("tool_quality", 3)

	s.Complete("task_completion", 2)
	s.Complete("policy", 1)
	s.Fail("tool_quality")

	sum := s.Summary()
	if sum.Total != 3 || sum.Done != 2 || sum.Failed != 1 || sum.Pending != 0 {
		t.Errorf("summary = %+v, want Total3 Done2 Failed1 Pending0", sum)
	}
	if got := sum.String(); got != "2 done, 1 failed" {
		t.Errorf("summary string = %q, want '2 done, 1 failed'", got)
	}
	if s.Functions["task_completion"].ScoreCount != 2 {
		t.Errorf("score count not recorded")
	}
}

func TestSummaryStringEmpty(t *testing.T) {
	s := NewState("trace-1", "turn")
	if got := s.Summary().String(); got != "none" {
		t.Errorf("empty summary string = %q, want 'none'", got)
	}
}
