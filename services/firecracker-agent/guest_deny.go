package main

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/firecracker"
)

// guestDenyRecheck is how often the shared guest deny chains are
// re-detected and re-asserted. Matches the preflight re-check cadence.
// Every VM create also asserts them synchronously (and fails closed), so
// this loop only has to catch host firewall reloads and address changes
// between creates.
const guestDenyRecheck = 30 * time.Second

// maintainGuestDeny keeps the guest private-destination deny guard
// installed until ctx is done and feeds its state into the readiness
// gate: the agent is NOT_SERVING until the guard is first verified, and
// again after two consecutive failed asserts, refusing VM work. See
// internal/sandbox/firecracker/guest_deny.go.
func maintainGuestDeny(ctx context.Context, gate *readyGate) {
	firecracker.MaintainGuestDeny(ctx, guestDenyRecheck, func(err error) {
		gate.reportGuard(err == nil)
	})
}
