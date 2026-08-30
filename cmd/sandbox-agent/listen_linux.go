//go:build linux

package main

import (
	"net"

	"github.com/mdlayher/vsock"
)

// listenVsock binds an AF_VSOCK listener on CID=any, port=listenPort.
// On Firecracker, the host-side UDS proxy bridges host Unix socket traffic
// to this listener when a host writes "CONNECT <port>\n".
func listenVsock() (net.Listener, error) {
	return vsock.Listen(listenPort, nil)
}
