package mcp

import (
	"errors"
	"sync/atomic"
)

// ErrStdioNotAllowed is returned whenever a stdio-transport server is
// refused because stdio is disabled for this process (see DisallowStdio).
// Its text is customer-facing: the Connect handlers return it verbatim, so
// every refusal (register, update, lazy start) reads the same. Callers map
// it to a client error; it must never be retried, because the refusal is a
// deployment policy, not a transient fault.
var ErrStdioNotAllowed = errors.New("stdio transport is not available on shared gateways; use sse or streamable_http")

// stdioDisabled is the process-wide stdio switch. It is package state, not
// per-Registry state, so that every construction path of the stdio
// transport (NewTransport, NewClient, Registry.Register, and the exec in
// stdioTransport.start) consults the same answer: a stdio server is a child
// process of the gateway itself, and "this process serves more than one
// tenant" is a property of the process, not of any one registry.
var stdioDisabled atomic.Bool

// DisallowStdio refuses every stdio-transport server for the rest of the
// life of this process. Intended for gateways that serve more than one
// tenant, where a tenant-supplied command would run inside the shared
// gateway process with its privileges. The switch is one-way.
func DisallowStdio() {
	stdioDisabled.Store(true)
}

// StdioAllowed reports whether stdio-transport servers may be constructed
// or started in this process.
func StdioAllowed() bool {
	return !stdioDisabled.Load()
}

// SetStdioPolicyForTest overrides the process-wide switch and returns a
// function that restores the previous value. Tests only: production code
// must use DisallowStdio, which is deliberately one-way. Tests that call it
// must not run in parallel with tests that spawn stdio servers.
func SetStdioPolicyForTest(allowed bool) (restore func()) {
	prev := stdioDisabled.Swap(!allowed)
	return func() { stdioDisabled.Store(prev) }
}
