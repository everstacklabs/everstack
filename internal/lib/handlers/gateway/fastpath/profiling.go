package fastpath

import (
	"fmt"
	"runtime"
	"runtime/pprof"
	"time"
)

// MemoryStats contains memory usage statistics for the fast-path engine.
type MemoryStats struct {
	// Timestamp when stats were collected
	Timestamp time.Time
	
	// Go runtime memory stats
	Alloc        uint64 // bytes allocated and still in use
	TotalAlloc   uint64 // bytes allocated (even if freed)
	Sys          uint64 // bytes obtained from system
	NumGC        uint32 // number of completed GC cycles
	PauseTotalNs uint64 // cumulative nanoseconds in GC stop-the-world pauses
	
	// Fast-path specific stats
	AuthCacheSize     int
	RouterCacheSize   int
	ExactCacheSize    int
	SemanticCacheSize int
	
	// Estimated memory usage per component (bytes)
	AuthCacheBytes     uint64
	RouterCacheBytes   uint64
	ExactCacheBytes    uint64
	SemanticCacheBytes uint64
	TotalCacheBytes    uint64
}

// GetMemoryStats collects current memory statistics.
func (e *Engine) GetMemoryStats() MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	stats := MemoryStats{
		Timestamp:    time.Now(),
		Alloc:        m.Alloc,
		TotalAlloc:   m.TotalAlloc,
		Sys:          m.Sys,
		NumGC:        m.NumGC,
		PauseTotalNs: m.PauseTotalNs,
	}
	
	// Get cache sizes
	if e.authCache != nil {
		_, _, ratio := e.authCache.Stats()
		stats.AuthCacheSize = int(ratio * 100) // Approximate
		// Bloom filter: ~144KB for 100K keys at 0.1% FP rate
		// sync.Map: ~40 bytes per entry (key + value + overhead)
		stats.AuthCacheBytes = 144 * 1024 // Bloom filter base
		stats.AuthCacheBytes += uint64(stats.AuthCacheSize * 40) // sync.Map entries
	}
	
	if e.routerCache != nil {
		hits, misses, _ := e.routerCache.Stats()
		stats.RouterCacheSize = int(hits + misses)
		// Each ProviderInfo: ~200 bytes (strings + struct overhead)
		stats.RouterCacheBytes = uint64(stats.RouterCacheSize * 200)
	}
	
	if e.exactCache != nil {
		_, _, _, _, size := e.exactCache.Stats()
		stats.ExactCacheSize = size
		// Estimate: 10KB per cached response (average)
		stats.ExactCacheBytes = uint64(size * 10 * 1024)
	}
	
	if e.semanticCache != nil {
		stats.SemanticCacheSize = e.semanticCache.Size()
		// MinHash: ~1KB per entry (signature + metadata)
		stats.SemanticCacheBytes = uint64(stats.SemanticCacheSize * 1024)
	}
	
	stats.TotalCacheBytes = stats.AuthCacheBytes + stats.RouterCacheBytes + 
		stats.ExactCacheBytes + stats.SemanticCacheBytes
	
	return stats
}

// FormatMemoryStats returns a human-readable string of memory stats.
func (s MemoryStats) String() string {
	return fmt.Sprintf(`Memory Statistics (collected at %s)
Runtime:
  Allocated: %s
  Total Allocated: %s
  System: %s
  GC Cycles: %d
  GC Pause Total: %s

Fast-Path Caches:
  Auth Cache: %d entries, %s
  Router Cache: %d entries, %s
  Exact Cache: %d entries, %s
  Semantic Cache: %d entries, %s
  Total Cache Memory: %s
`,
		s.Timestamp.Format(time.RFC3339),
		formatBytes(s.Alloc),
		formatBytes(s.TotalAlloc),
		formatBytes(s.Sys),
		s.NumGC,
		time.Duration(s.PauseTotalNs),
		s.AuthCacheSize, formatBytes(s.AuthCacheBytes),
		s.RouterCacheSize, formatBytes(s.RouterCacheBytes),
		s.ExactCacheSize, formatBytes(s.ExactCacheBytes),
		s.SemanticCacheSize, formatBytes(s.SemanticCacheBytes),
		formatBytes(s.TotalCacheBytes),
	)
}

// formatBytes formats bytes as human-readable string.
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// AllocationTracker tracks memory allocations during a specific operation.
type AllocationTracker struct {
	start  runtime.MemStats
	end    runtime.MemStats
	name   string
	active bool
}

// StartAllocationTracking begins tracking memory allocations.
func StartAllocationTracking(name string) *AllocationTracker {
	tracker := &AllocationTracker{
		name:   name,
		active: true,
	}
	runtime.ReadMemStats(&tracker.start)
	return tracker
}

// Stop stops tracking and returns allocation statistics.
func (t *AllocationTracker) Stop() AllocationReport {
	if !t.active {
		return AllocationReport{}
	}
	
	runtime.ReadMemStats(&t.end)
	t.active = false
	
	return AllocationReport{
		Name:           t.name,
		AllocBytes:     t.end.Alloc - t.start.Alloc,
		TotalAllocated: t.end.TotalAlloc - t.start.TotalAlloc,
		Mallocs:        t.end.Mallocs - t.start.Mallocs,
		Frees:          t.end.Frees - t.start.Frees,
		NumGC:          t.end.NumGC - t.start.NumGC,
	}
}

// AllocationReport contains allocation statistics for an operation.
type AllocationReport struct {
	Name           string
	AllocBytes     uint64 // Net allocation (alloc - free)
	TotalAllocated uint64 // Total bytes allocated
	Mallocs        uint64 // Number of mallocs
	Frees          uint64 // Number of frees
	NumGC          uint32 // Number of GC cycles
}

// String returns a human-readable report.
func (r AllocationReport) String() string {
	return fmt.Sprintf(`Allocation Report: %s
  Net Allocated: %s
  Total Allocated: %s
  Mallocs: %d
  Frees: %d
  GC Cycles: %d
  Avg Allocation Size: %s
`,
		r.Name,
		formatBytes(r.AllocBytes),
		formatBytes(r.TotalAllocated),
		r.Mallocs,
		r.Frees,
		r.NumGC,
		formatBytes(r.TotalAllocated/max(r.Mallocs, 1)),
	)
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// ProfileMemory captures a memory profile for analysis.
func ProfileMemory(filename string) error {
	f := pprof.Lookup("heap")
	if f == nil {
		return fmt.Errorf("heap profile not found")
	}
	
	// Force GC to get accurate profile
	runtime.GC()
	
	return f.WriteTo(&fileWriter{name: filename}, 0)
}

// ProfileCPU captures a CPU profile for analysis.
func ProfileCPU(filename string, duration time.Duration) error {
	f := &fileWriter{name: filename}
	
	if err := pprof.StartCPUProfile(f); err != nil {
		return fmt.Errorf("start CPU profile: %w", err)
	}
	
	time.Sleep(duration)
	pprof.StopCPUProfile()
	
	return nil
}

// fileWriter is a simple writer for pprof.
type fileWriter struct {
	name string
	data []byte
}

func (w *fileWriter) Write(p []byte) (n int, err error) {
	w.data = append(w.data, p...)
	return len(p), nil
}

// OptimizationRecommendations analyzes memory stats and provides recommendations.
func (s MemoryStats) OptimizationRecommendations() []string {
	recommendations := make([]string, 0)
	
	// Check if exact cache is too large
	if s.ExactCacheBytes > 1024*1024*1024 { // > 1GB
		recommendations = append(recommendations,
			fmt.Sprintf("Exact cache is using %s. Consider reducing max_entries or TTL.", 
				formatBytes(s.ExactCacheBytes)))
	}
	
	// Check if semantic cache is enabled but not used
	if s.SemanticCacheSize == 0 && s.SemanticCacheBytes > 0 {
		recommendations = append(recommendations,
			"Semantic cache is initialized but not used. Consider disabling it to save memory.")
	}
	
	// Check GC pressure
	gcPauseAvg := time.Duration(s.PauseTotalNs) / time.Duration(max(uint64(s.NumGC), 1))
	if gcPauseAvg > 5*time.Millisecond {
		recommendations = append(recommendations,
			fmt.Sprintf("Average GC pause is %s. Consider tuning GOGC or reducing allocations.",
				gcPauseAvg))
	}
	
	// Check total cache memory
	if s.TotalCacheBytes > 2*1024*1024*1024 { // > 2GB
		recommendations = append(recommendations,
			fmt.Sprintf("Total cache memory is %s. Consider implementing cache eviction or using Redis.",
				formatBytes(s.TotalCacheBytes)))
	}
	
	// Check system memory vs allocated
	if s.Sys > s.Alloc*2 {
		recommendations = append(recommendations,
			fmt.Sprintf("System memory (%s) is much larger than allocated (%s). Memory may be fragmented.",
				formatBytes(s.Sys), formatBytes(s.Alloc)))
	}
	
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Memory usage looks healthy. No optimizations needed.")
	}
	
	return recommendations
}

