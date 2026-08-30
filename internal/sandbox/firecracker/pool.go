package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// VMPoolConfig controls the warm VM pool behavior.
type VMPoolConfig struct {
	MinSize           int           `json:"min_size"`           // Minimum warm VMs to maintain
	MaxSize           int           `json:"max_size"`           // Maximum warm VMs
	MaxTotal          int           `json:"max_total"`          // Hard cap on total VMs (warm + active)
	IdleTimeout       time.Duration `json:"idle_timeout"`       // Reclaim warm VMs after idle
	WarmupOnStart     bool          `json:"warmup_on_start"`    // Pre-boot VMs on startup
	ReplenishInterval time.Duration `json:"replenish_interval"` // How often to check and replenish (default: 1s)
	ReplenishBatch    int           `json:"replenish_batch"`    // Max VMs to create per replenish tick (default: 3)
}

// DefaultVMPoolConfig returns sensible defaults.
func DefaultVMPoolConfig() VMPoolConfig {
	return VMPoolConfig{
		MinSize:           2,
		MaxSize:           10,
		MaxTotal:          50,
		IdleTimeout:       5 * time.Minute,
		WarmupOnStart:     false,
		ReplenishInterval: 1 * time.Second,
		ReplenishBatch:    3,
	}
}

// Validate enforces invariants that, when violated, silently break
// the pool at runtime. Most importantly: MaxSize must not exceed
// MaxTotal — otherwise the warm pool tries to outgrow the hard cap
// and the replenish loop spins on a permanently-failing CreateVM.
//
// Memory budgeting (MaxTotal × VM.MemoryMB ≤ cgroup limit) lives at
// the helm layer, not here, since this struct doesn't know the
// cgroup limit. The pool guard catches the in-process bookkeeping
// inversions.
func (c VMPoolConfig) Validate() error {
	if c.MinSize < 0 {
		return fmt.Errorf("pool.MinSize must be ≥ 0, got %d", c.MinSize)
	}
	if c.MaxSize < 0 {
		return fmt.Errorf("pool.MaxSize must be ≥ 0, got %d", c.MaxSize)
	}
	if c.MaxTotal <= 0 {
		return fmt.Errorf("pool.MaxTotal must be > 0, got %d", c.MaxTotal)
	}
	if c.MinSize > c.MaxSize {
		return fmt.Errorf("pool.MinSize (%d) must be ≤ MaxSize (%d)", c.MinSize, c.MaxSize)
	}
	if c.MaxSize > c.MaxTotal {
		return fmt.Errorf("pool.MaxSize (%d) must be ≤ MaxTotal (%d) — warm pool cannot exceed hard cap", c.MaxSize, c.MaxTotal)
	}
	if c.WarmupOnStart && c.MinSize > c.MaxTotal {
		return fmt.Errorf("pool.WarmupOnStart=true with MinSize (%d) > MaxTotal (%d) would block forever", c.MinSize, c.MaxTotal)
	}
	if c.ReplenishInterval < 0 {
		return fmt.Errorf("pool.ReplenishInterval must be ≥ 0, got %s", c.ReplenishInterval)
	}
	if c.ReplenishBatch < 0 {
		return fmt.Errorf("pool.ReplenishBatch must be ≥ 0, got %d", c.ReplenishBatch)
	}
	return nil
}

// VMPool manages a pool of pre-booted Firecracker VMs for fast sandbox creation.
// Pre-booting reduces sandbox creation from ~125ms boot time to ~10ms acquire.
type VMPool struct {
	config FirecrackerConfig
	warm   []*MicroVM // Pre-booted, idle VMs
	active int        // Count of in-use VMs
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// baseSize caches the stat of base.ext4; see baseRootfsSize.
	baseSize atomic.Int64
}

// MaxTotal returns the configured hard cap on warm + active VMs.
// Exposed for the host-pressure collector which uses remaining
// capacity as a placement-gating input.
func (p *VMPool) MaxTotal() int {
	if p == nil {
		return 0
	}
	return p.config.Pool.MaxTotal
}

// baseRootfsSize returns the size of the pool's base rootfs image, which
// is what a warm VM's disk is, since warm VMs are pre-booted before any
// request's DiskMB is known.
//
// Cached after the first successful stat: the file is written once at
// pod startup by the rootfs-build init container and never changes
// underneath a running agent.
//
// Returns 0 when the image cannot be stat'd. That is the safe direction:
// callers compare a requested size against it, so 0 makes every
// disk-specifying request cold-start rather than risk handing out a warm
// VM that is smaller than what was asked for.
func (p *VMPool) baseRootfsSize() int64 {
	if p == nil {
		return 0
	}
	if sz := p.baseSize.Load(); sz != 0 {
		return sz
	}
	info, err := os.Stat(filepath.Join(p.config.RootfsDir, "base.ext4"))
	if err != nil {
		return 0
	}
	p.baseSize.Store(info.Size())
	return info.Size()
}

// NewVMPool creates and optionally warms a VM pool.
func NewVMPool(cfg FirecrackerConfig) *VMPool {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &VMPool{
		config: cfg,
		warm:   make([]*MicroVM, 0, cfg.Pool.MaxSize),
		cancel: cancel,
		done:   make(chan struct{}),
	}

	if cfg.Pool.WarmupOnStart {
		go pool.warmup(ctx)
	}

	go pool.replenish(ctx)

	return pool
}

// Acquire returns a VM from the pool (warm) or creates a new one (cold).
//
// Warm pool VMs are pre-booted with the default resource config (VM.VCPUs / VM.MemoryMB).
// When the requested InstanceConfig specifies different CPU or memory, the warm pool is
// bypassed and a cold VM is created with the exact requested resources. This ensures
// user-selected resource tiers (via sandbox templates) get the correct sizing.
//
// Future optimization: tiered warm pools keyed by (vcpu, memory) for instant acquisition
// of popular non-default sizes.
func (p *VMPool) Acquire(ctx context.Context, id string, config sandbox.InstanceConfig) (*MicroVM, error) {
	networkMode, err := normalizeNetworkMode(config.NetworkMode)
	if err != nil {
		return nil, err
	}
	config.NetworkMode = networkMode

	p.mu.Lock()

	// Check total limit
	total := len(p.warm) + p.active
	if total >= p.config.Pool.MaxTotal {
		p.mu.Unlock()
		return nil, fmt.Errorf("VM pool exhausted (%d total VMs)", p.config.Pool.MaxTotal)
	}

	// Determine if requested resources match the pool's default VM size.
	// Warm VMs are booted with config.VM defaults; if the request asks for
	// different CPU/memory, we must cold-start to get the right size.
	// DiskMB counts too: warm VMs are booted from an un-grown clone of
	// base.ext4, and the disk cannot be resized once firecracker has
	// the drive open. Handing a warm VM to a request that asked for
	// more disk would silently under-provision it (and still bill for
	// the larger size), so those requests cold-start.
	needsCustomSize := (config.CPULimit > 0 && int(config.CPULimit) != p.config.VM.VCPUs) ||
		(config.MemoryMB > 0 && config.MemoryMB != p.config.VM.MemoryMB) ||
		(config.DiskMB > 0 && config.DiskMB*1024*1024 > p.baseRootfsSize())
	needsCustomNetwork := config.NetworkMode != sandbox.NetworkAllow || len(config.AllowedHosts) > 0

	// Try warm VM first (only if default size matches)
	if !needsCustomSize && !needsCustomNetwork && len(p.warm) > 0 {
		vm := p.warm[len(p.warm)-1]
		p.warm = p.warm[:len(p.warm)-1]
		p.active++
		p.mu.Unlock()

		// Reconfigure the warm VM for this sandbox
		vm.ID = id
		vm.Config = config
		if vm.Network == nil {
			p.Release(context.Background(), vm)
			return nil, fmt.Errorf("warm VM has no network")
		}
		if len(config.DNSServers) > 0 {
			vm.Network.DNSServers = append([]string(nil), config.DNSServers...)
			var resolv strings.Builder
			for _, ns := range vm.Network.GuestResolvers() {
				resolv.WriteString("nameserver " + ns + "\n")
			}
			if err := vm.ToolboxWriteFile(ctx, "/etc/resolv.conf", []byte(resolv.String())); err != nil {
				p.Release(context.Background(), vm)
				return nil, fmt.Errorf("failed to refresh warm VM DNS config: %w", err)
			}
		}
		logger.WithFields("sandbox_id", id).Debug("vm_pool: acquired warm VM")
		return vm, nil
	}

	p.active++
	p.mu.Unlock()

	// Cold start: create a new VM with exact requested resources
	vm, err := CreateVM(p.config, id, config)
	if err != nil {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, err
	}

	if needsCustomSize {
		logger.WithFields("sandbox_id", id, "vcpus", vm.VCPUs, "memory_mb", vm.MemoryMB).
			Debug("vm_pool: cold started VM with custom resources")
	} else {
		logger.WithFields("sandbox_id", id).Debug("vm_pool: cold started VM (pool empty)")
	}
	return vm, nil
}

// TrackedPIDs returns the PIDs of every warm-pool VM under the
// pool's lock. Used by the orphan reaper to distinguish a
// child-of-fcagent firecracker process we know about from one we
// lost track of (and therefore should kill).
func (p *VMPool) TrackedPIDs() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	pids := make([]int, 0, len(p.warm))
	for _, vm := range p.warm {
		if vm != nil && vm.Process != nil {
			pids = append(pids, vm.Process.Pid)
		}
	}
	return pids
}

// TrackedIDs returns the sandbox IDs of every warm-pool VM. Used by
// the workdir reconciler to keep pool slots out of the orphan list
// (their workdirs are real and in-use, just not assigned to a user
// sandbox yet).
func (p *VMPool) TrackedIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.warm))
	for _, vm := range p.warm {
		if vm != nil {
			ids = append(ids, vm.ID)
		}
	}
	return ids
}

// Release returns a VM to the pool or destroys it.
// Sandboxes are ephemeral per session, so VMs are always destroyed (not reused).
func (p *VMPool) Release(ctx context.Context, vm *MicroVM) {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()

	// Sandboxes modify the rootfs, so VMs cannot be reused.
	if err := vm.Destroy(ctx); err != nil {
		logger.WithFields("vm_id", vm.ID, "error", err.Error()).
			Warn("vm_pool: failed to destroy released VM")
	}
}

// warmup pre-boots the minimum number of VMs.
func (p *VMPool) warmup(ctx context.Context) {
	for i := 0; i < p.config.Pool.MinSize; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		id := fmt.Sprintf("warm_%d", i)
		vm, err := CreateVM(p.config, id, sandbox.InstanceConfig{
			Image:       p.config.RootfsDir + "/base.ext4",
			WorkDir:     "/workspace",
			NetworkMode: sandbox.NetworkAllow,
		})
		if err != nil {
			logger.WithFields("error", err.Error()).
				Warn("vm_pool: warmup failed to create VM")
			continue
		}

		p.mu.Lock()
		if len(p.warm) < p.config.Pool.MaxSize {
			p.warm = append(p.warm, vm)
		} else {
			go vm.Destroy(context.Background())
		}
		p.mu.Unlock()
	}

	logger.WithFields("count", p.config.Pool.MinSize).
		Info("vm_pool: warmup complete")
}

// replenish maintains the minimum number of warm VMs.
// Uses configurable ReplenishInterval (default 1s) and creates up to
// ReplenishBatch VMs per tick (default 3) to recover faster from bursts.
func (p *VMPool) replenish(ctx context.Context) {
	defer close(p.done)

	interval := p.config.Pool.ReplenishInterval
	if interval <= 0 {
		interval = 1 * time.Second
	}
	batchSize := p.config.Pool.ReplenishBatch
	if batchSize <= 0 {
		batchSize = 3
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			deficit := p.config.Pool.MinSize - len(p.warm)
			total := len(p.warm) + p.active
			p.mu.Unlock()

			if deficit <= 0 || total >= p.config.Pool.MaxTotal {
				continue
			}

			// Create up to batchSize VMs per tick, capped by deficit and MaxTotal headroom.
			toCreate := deficit
			if toCreate > batchSize {
				toCreate = batchSize
			}
			headroom := p.config.Pool.MaxTotal - total
			if toCreate > headroom {
				toCreate = headroom
			}

			for i := 0; i < toCreate; i++ {
				id := fmt.Sprintf("warm_%d_%d", time.Now().UnixNano(), i)
				vm, err := CreateVM(p.config, id, sandbox.InstanceConfig{
					Image:       p.config.RootfsDir + "/base.ext4",
					WorkDir:     "/workspace",
					NetworkMode: sandbox.NetworkAllow,
				})
				if err != nil {
					break // stop batch on first failure
				}

				p.mu.Lock()
				if len(p.warm) < p.config.Pool.MaxSize {
					p.warm = append(p.warm, vm)
				} else {
					go vm.Destroy(context.Background())
				}
				p.mu.Unlock()
			}
		}
	}
}

// Stop shuts down the pool and destroys all warm VMs.
func (p *VMPool) Stop() {
	p.cancel()
	<-p.done

	p.mu.Lock()
	warm := p.warm
	p.warm = nil
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, vm := range warm {
		_ = vm.Destroy(ctx)
	}
}
