package retrypolicy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoffHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	if err := Backoff(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backoff = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Backoff ignored cancellation for %v", elapsed)
	}
	if err := Backoff(context.Background(), 0); err != nil {
		t.Fatalf("zero backoff = %v", err)
	}
}

func TestJitterBounds(t *testing.T) {
	d := 100 * time.Millisecond
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		j := Jitter(d)
		if j < d/2 || j >= d+d/2 {
			t.Fatalf("Jitter(%v) = %v, outside [d/2, 3d/2)", d, j)
		}
		seen[j] = true
	}
	if len(seen) < 2 {
		t.Fatal("Jitter returned a constant")
	}
}
