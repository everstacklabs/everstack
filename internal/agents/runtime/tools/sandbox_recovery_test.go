package tools

import (
	"errors"
	"fmt"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestIsDeadGuestToolError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed not-running", sandbox.ErrSandboxNotRunning, true},
		{"wrapped not-running", fmt.Errorf("exec: %w", sandbox.ErrSandboxNotRunning), true},
		{"vsock gone (the browser failure)", errors.New("browser: sidecar not ready: ... dial unix /var/lib/everstack/vms/wks_x/vsock.sock: connect: no such file or directory"), true},
		{"guest agent unreachable", errors.New("failed to connect to guest agent"), true},
		{"connection refused", errors.New("rpc error: connection refused"), true},
		{"vm not found", errors.New("status 1.2.3.4:9092: VM not found for sandbox sbx_x"), true},
		{"benign tool error", errors.New("old_string not found in file"), false},
		{"benign timeout", errors.New("context deadline exceeded"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDeadGuestToolError(c.err); got != c.want {
				t.Fatalf("isDeadGuestToolError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
