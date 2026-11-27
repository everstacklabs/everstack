package firecracker

import (
	"github.com/prometheus/client_golang/prometheus"
)

// RegisterVMStoreMetrics exposes VM store headroom next to the create
// guard's threshold, so an alert can fire before the guard starts
// refusing creates and wakes:
//
//   - everstack_fcagent_vmstore_free_bytes: statfs Bavail of workDir,
//     the same number checkDiskHeadroom compares against.
//   - everstack_fcagent_vmstore_size_bytes: total size of that filesystem.
//   - everstack_fcagent_disk_guard_min_free_bytes: minFreeMB in bytes
//     (0 when the guard is disabled).
//
// The values are read at scrape time; statfs is one syscall. When statfs
// fails the free and size series are omitted for that scrape rather than
// reported as 0, which would look like a full disk.
func RegisterVMStoreMetrics(reg prometheus.Registerer, workDir string, minFreeMB int64) error {
	guardBytes := float64(0)
	if minFreeMB > 0 {
		guardBytes = float64(minFreeMB) * 1024 * 1024
	}
	collectors := []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: "everstack",
			Subsystem: "fcagent",
			Name:      "disk_guard_min_free_bytes",
			Help:      "Free space the VM store must keep before a create or wake is admitted (FC_MIN_FREE_DISK_MB). 0 means the guard is off.",
		}, func() float64 { return guardBytes }),
		&vmStoreCollector{
			workDir: workDir,
			free: prometheus.NewDesc("everstack_fcagent_vmstore_free_bytes",
				"Bytes available to the agent on the VM store filesystem (statfs Bavail, what the create guard reads).", nil, nil),
			size: prometheus.NewDesc("everstack_fcagent_vmstore_size_bytes",
				"Total bytes of the VM store filesystem.", nil, nil),
		},
	}
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

type vmStoreCollector struct {
	workDir    string
	free, size *prometheus.Desc
}

func (c *vmStoreCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.free
	ch <- c.size
}

func (c *vmStoreCollector) Collect(ch chan<- prometheus.Metric) {
	total, avail, err := storeBytes(c.workDir)
	if err != nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.free, prometheus.GaugeValue, float64(avail))
	ch <- prometheus.MustNewConstMetric(c.size, prometheus.GaugeValue, float64(total))
}
