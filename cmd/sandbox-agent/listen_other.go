//go:build !linux

package main

import (
	"fmt"
	"net"
	"os"
)

// listenVsock on non-Linux falls back to a Unix socket so the agent still
// builds for local development. The real deployment target is linux (inside
// a Firecracker guest) where the AF_VSOCK implementation in listen_linux.go
// is used.
func listenVsock() (net.Listener, error) {
	sockPath := fmt.Sprintf("/tmp/sandbox-agent.%d.sock", listenPort)
	_ = os.Remove(sockPath)
	return net.Listen("unix", sockPath)
}
