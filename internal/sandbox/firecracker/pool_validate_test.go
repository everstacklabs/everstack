package firecracker

import (
	"strings"
	"testing"
	"time"
)

func TestVMPoolConfig_Validate(t *testing.T) {
	good := VMPoolConfig{
		MinSize: 2, MaxSize: 10, MaxTotal: 50,
		IdleTimeout: 5 * time.Minute, ReplenishBatch: 3, ReplenishInterval: time.Second,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}

	cases := []struct {
		name    string
		mut     func(c *VMPoolConfig)
		wantSub string
	}{
		{"max_size_exceeds_total", func(c *VMPoolConfig) { c.MaxSize = 100 }, "MaxSize"},
		{"min_exceeds_max", func(c *VMPoolConfig) { c.MinSize = 20 }, "MinSize"},
		{"zero_total", func(c *VMPoolConfig) { c.MaxTotal = 0 }, "MaxTotal"},
		{"negative_replenish", func(c *VMPoolConfig) { c.ReplenishBatch = -1 }, "ReplenishBatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q missing %q", err, tc.wantSub)
			}
		})
	}
}
