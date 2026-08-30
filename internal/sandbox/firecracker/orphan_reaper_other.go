//go:build !linux

package firecracker

// Non-Linux builds (developer macOS, CI on darwin) don't run
// firecracker — so there are no orphans to find. The reaper still
// gets started on the agent process for code-path uniformity; this
// stub returns an empty slice and nil error so the sweep is a no-op.
func findFirecrackerChildren(_ int) ([]firecrackerChild, error) {
	return nil, nil
}
