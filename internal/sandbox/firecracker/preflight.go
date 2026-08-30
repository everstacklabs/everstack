package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PreflightResult captures the outcome of a single preflight probe.
// Stored in a slice so callers can log every failure in one pass
// instead of fixing one, restarting, hitting the next.
type PreflightResult struct {
	Name string
	OK   bool
	Err  error
}

// Preflight runs every prerequisite check the firecracker backend
// needs to create VMs. Returns the per-probe results plus a combined
// error that is non-nil iff any probe failed. The agent's gRPC health
// stays NOT_SERVING until this returns nil; on regression we demote
// back to NOT_SERVING so the gateway routes traffic elsewhere instead
// of failing creates against a host that lost its rootfs/kernel.
func Preflight(cfg FirecrackerConfig) ([]PreflightResult, error) {
	results := []PreflightResult{
		probe("firecracker_binary", func() error { return validateBinaryExists(cfg.BinaryPath) }),
		probe("kvm_device", validateKVMAccess),
		probe("kernel_image", func() error { return checkReadableFile(cfg.KernelPath) }),
		probe("rootfs_dir", func() error { return checkRootfsDir(cfg.RootfsDir) }),
		probe("work_dir_writable", func() error { return checkWritableDir(cfg.WorkDir) }),
	}

	var failures []string
	for _, r := range results {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("%s: %v", r.Name, r.Err))
		}
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("preflight failed: %s", strings.Join(failures, "; "))
	}
	return results, nil
}

func probe(name string, fn func() error) PreflightResult {
	if err := fn(); err != nil {
		return PreflightResult{Name: name, OK: false, Err: err}
	}
	return PreflightResult{Name: name, OK: true}
}

func checkReadableFile(path string) error {
	if path == "" {
		return fmt.Errorf("path empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, expected file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_ = f.Close()
	return nil
}

// checkRootfsDir requires the directory to exist and contain at
// least one *.ext4 image — otherwise the agent will accept Create
// requests and then fail every one with "no rootfs found".
func checkRootfsDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("path empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".ext4") {
			return nil
		}
	}
	return fmt.Errorf("no .ext4 rootfs images found in %s", dir)
}

// checkWritableDir verifies the directory exists (creating it if
// missing) and that we can actually create a file there. A read-only
// mount is the operational shape of "PVC failed to attach"; we want
// that surfaced at startup, not on the first VM create.
func checkWritableDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("path empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	probe := filepath.Join(dir, ".preflight-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write probe %s: %w", probe, err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}
