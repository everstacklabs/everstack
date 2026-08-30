package firecracker

import (
	"context"
	"errors"
)

// Firecracker microVM snapshot API request bodies.
//
// API reference (Firecracker v1.x):
//   PATCH /vm                       — pause/resume VM state
//   PUT   /snapshot/create          — write snapshot files to host disk
//   PUT   /snapshot/load            — load snapshot on a fresh firecracker
//
// These types mirror the JSON shape exactly; field tags match the
// Firecracker spec so we don't accidentally rename through.

type vmStateBody struct {
	State string `json:"state"`
}

// SnapshotType is the kind of snapshot to take.
type SnapshotType string

const (
	// SnapshotTypeFull captures the full VM memory + state. Slower
	// + larger, but standalone (no base required to load).
	SnapshotTypeFull SnapshotType = "Full"
	// SnapshotTypeDiff captures only memory pages that have changed
	// since the last full snapshot. Smaller, but loading requires
	// applying the diff on top of the base snapshot. Reserved for a
	// future incremental-snapshot pass.
	SnapshotTypeDiff SnapshotType = "Diff"
)

type createSnapshotBody struct {
	SnapshotPath string       `json:"snapshot_path"`
	MemFilePath  string       `json:"mem_file_path"`
	SnapshotType SnapshotType `json:"snapshot_type,omitempty"`
}

type loadSnapshotBody struct {
	SnapshotPath        string                     `json:"snapshot_path"`
	MemFilePath         string                     `json:"mem_file_path"`
	EnableDiffSnapshots bool                       `json:"enable_diff_snapshots,omitempty"`
	ResumeVM            bool                       `json:"resume_vm"`
	NetworkInterfaces   []networkInterfaceOverride `json:"network_interfaces,omitempty"`
}

// networkInterfaceOverride remaps a snapshot's host_dev_name to a
// freshly-allocated TAP on the restoring host. The iface_id and
// guest_mac come from the snapshot itself and must match — we only
// override what's host-local.
type networkInterfaceOverride struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
}

// Pause transitions a running microVM to the Paused state. Required
// before CreateSnapshot — Firecracker refuses to snapshot a running VM
// because memory pages would be in flux. Calling on an already-paused
// VM is a no-op error from the API; treat 4xx as fatal so callers know
// not to proceed with snapshot.
func (c *vmAPIClient) Pause(ctx context.Context) error {
	if c == nil {
		return errors.New("firecracker: nil vmAPIClient")
	}
	return c.patch(ctx, "/vm", vmStateBody{State: "Paused"})
}

// Resume restores a paused VM. Used after a successful CreateSnapshot
// so the agent can continue handling traffic while the snapshot
// uploads in the background.
func (c *vmAPIClient) Resume(ctx context.Context) error {
	if c == nil {
		return errors.New("firecracker: nil vmAPIClient")
	}
	return c.patch(ctx, "/vm", vmStateBody{State: "Resumed"})
}

// CreateSnapshot writes the snapshot + memory file to local paths on
// the host. The microVM MUST be paused first; CreateSnapshot itself
// does not pause. Caller is responsible for streaming the resulting
// files to object storage and cleaning up the local copies.
func (c *vmAPIClient) CreateSnapshot(ctx context.Context, snapshotPath, memFilePath string, kind SnapshotType) error {
	if c == nil {
		return errors.New("firecracker: nil vmAPIClient")
	}
	if snapshotPath == "" || memFilePath == "" {
		return errors.New("firecracker: snapshotPath and memFilePath required")
	}
	body := createSnapshotBody{
		SnapshotPath: snapshotPath,
		MemFilePath:  memFilePath,
	}
	if kind != "" {
		body.SnapshotType = kind
	}
	return c.put(ctx, "/snapshot/create", body)
}

// NetworkOverride describes a single (iface_id, new host TAP) mapping
// for LoadSnapshot when the restoring host's TAP name doesn't match
// the one captured in the snapshot.
type NetworkOverride struct {
	IfaceID     string
	HostDevName string
}

// LoadSnapshot rehydrates a paused microVM on a freshly-started
// Firecracker process. The target firecracker must already have its
// API socket bound but NOT have a microVM configured (no PUT
// /machine-config, no PUT /boot-source) — the snapshot supplies all
// of that. resumeVM=true transitions straight to Running; false
// leaves the VM in Paused so the caller can do further setup before
// resuming.
//
// netOverrides remaps host_dev_name per iface_id so a fresh TAP on
// the restoring host can pick up where the snapshot's TAP left off.
// The guest MAC stays bound to the iface — that's captured in the
// snapshot — so the guest kernel sees the interface as unchanged.
func (c *vmAPIClient) LoadSnapshot(ctx context.Context, snapshotPath, memFilePath string, resumeVM bool, netOverrides ...NetworkOverride) error {
	if c == nil {
		return errors.New("firecracker: nil vmAPIClient")
	}
	if snapshotPath == "" || memFilePath == "" {
		return errors.New("firecracker: snapshotPath and memFilePath required")
	}
	body := loadSnapshotBody{
		SnapshotPath: snapshotPath,
		MemFilePath:  memFilePath,
		ResumeVM:     resumeVM,
	}
	if len(netOverrides) > 0 {
		body.NetworkInterfaces = make([]networkInterfaceOverride, 0, len(netOverrides))
		for _, n := range netOverrides {
			body.NetworkInterfaces = append(body.NetworkInterfaces, networkInterfaceOverride{
				IfaceID:     n.IfaceID,
				HostDevName: n.HostDevName,
			})
		}
	}
	return c.put(ctx, "/snapshot/load", body)
}
