package firecracker

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestVMStoreMetricsReportWhatTheGuardReads(t *testing.T) {
	dir := t.TempDir()
	reg := prometheus.NewRegistry()
	if err := RegisterVMStoreMetrics(reg, dir, 2048); err != nil {
		t.Fatalf("RegisterVMStoreMetrics: %v", err)
	}

	if got := gaugeValue(t, reg, "everstack_fcagent_disk_guard_min_free_bytes"); got != 2048*1024*1024 {
		t.Fatalf("guard gauge = %v, want %v", got, 2048*1024*1024)
	}

	avail, err := availableBytes(dir)
	if err != nil {
		t.Fatalf("availableBytes: %v", err)
	}
	free := gaugeValue(t, reg, "everstack_fcagent_vmstore_free_bytes")
	size := gaugeValue(t, reg, "everstack_fcagent_vmstore_size_bytes")
	if free <= 0 || size < free {
		t.Fatalf("free=%v size=%v", free, size)
	}
	// Same statfs field as the guard (Bavail), so the alert and the
	// refusal agree. Allow drift from other writers between the calls.
	if diff := free - float64(avail); diff > 64<<20 || diff < -64<<20 {
		t.Fatalf("free metric %v differs from guard's view %v", free, avail)
	}
}

func TestVMStoreMetricsGuardOffReportsZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := RegisterVMStoreMetrics(reg, t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, reg, "everstack_fcagent_disk_guard_min_free_bytes"); got != 0 {
		t.Fatalf("guard gauge = %v, want 0", got)
	}
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}
