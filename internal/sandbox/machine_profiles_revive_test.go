package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// createCapturingBackend records the config handed to Create and then fails,
// so ExecuteRevive stops right after the size decision under test.
type createCapturingBackend struct {
	Backend
	created []InstanceConfig
}

var errCaptureStop = errors.New("capture stop")

func (b *createCapturingBackend) Name() string { return "capture" }

func (b *createCapturingBackend) Create(_ context.Context, _ string, config InstanceConfig) (*Instance, error) {
	b.created = append(b.created, config)
	return nil, errCaptureStop
}

// TestExecuteReviveClampsSavedSizeToCurrentPlan reproduces the downgrade
// bypass: a legacy 4 vCPU / 512 MB sandbox passed the memory-only check and
// was revived with all 4 vCPUs on Free, whose largest profile is 0.5 vCPU.
func TestExecuteReviveClampsSavedSizeToCurrentPlan(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		tier     string
		saved    InstanceConfig
		wantCPU  float64
		wantMem  int64
		wantDisk int64
		wantErr  bool
	}{
		{name: "legacy 4 vCPU on free", tier: "free", saved: InstanceConfig{CPULimit: 4, MemoryMB: 512, DiskMB: 1024}, wantCPU: 0.5, wantMem: 512, wantDisk: 1024},
		{name: "large profile after downgrade to basic", tier: "basic", saved: InstanceConfig{CPULimit: 4, MemoryMB: 4096, DiskMB: 20480}, wantCPU: 1, wantMem: 1024, wantDisk: 20480},
		{name: "within plan kept as saved", tier: "pro", saved: InstanceConfig{CPULimit: 1, MemoryMB: 1024, DiskMB: 1024}, wantCPU: 1, wantMem: 1024, wantDisk: 1024},
		{name: "disk above plan refused", tier: "enterprise", saved: InstanceConfig{CPULimit: 4, MemoryMB: 4096, DiskMB: 51200}, wantErr: true},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rawDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer rawDB.Close()

			saved := tt.saved
			saved.TenantID = "tenant-1"
			saved.SessionID = "sess-1"
			configJSON, _ := json.Marshal(saved)
			mock.ExpectQuery(`SELECT config, image FROM sandbox_instances`).
				WithArgs("sbx-1").
				WillReturnRows(sqlmock.NewRows([]string{"config", "image"}).AddRow(configJSON, "img"))
			if tt.tier != "enterprise" {
				mock.ExpectQuery(`SELECT id\s+FROM sandbox_instances`).
					WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}

			backend := &createCapturingBackend{}
			m := &SandboxManager{
				db:                 sqlx.NewDb(rawDB, "sqlmock"),
				backend:            backend,
				instances:          map[string]*Instance{},
				instancesBySandbox: map[string]*Instance{},
			}
			m.SetManagedMachineProfilesRequired(true)
			tier := tt.tier
			m.SetTenantTierResolver(func(string) string { return tier })

			_, err = m.ExecuteRevive(context.Background(), "sbx-1", "", "")
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedSandboxSize) {
					t.Fatalf("err = %v, want ErrUnsupportedSandboxSize", err)
				}
				if len(backend.created) != 0 {
					t.Fatalf("backend.Create called with %+v, want no allocation", backend.created[0])
				}
				return
			}
			if !errors.Is(err, errCaptureStop) {
				t.Fatalf("err = %v, want the capture stop from backend.Create", err)
			}
			got := backend.created[0]
			if got.CPULimit != tt.wantCPU || got.MemoryMB != tt.wantMem || got.DiskMB != tt.wantDisk {
				t.Fatalf("revived with (%v vCPU, %d MB, %d MB disk), want (%v, %d, %d)",
					got.CPULimit, got.MemoryMB, got.DiskMB, tt.wantCPU, tt.wantMem, tt.wantDisk)
			}
		})
	}
}

// TestNormalizedAgentSizeIsMeteredAtConfiguredSize reproduces the
// overcharge: a stored 0.25 vCPU / 256 MB agent is provisioned as nano
// (0.5 vCPU / 512 MB), and the meter priced the provisioned size, doubling
// the charge. The customer must be billed per dimension for no more than
// they configured.
func TestNormalizedAgentSizeIsMeteredAtConfiguredSize(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	m.SetManagedMachineProfilesRequired(true)

	stored := SandboxConfig{CPULimit: 0.25, MemoryMB: 256, DiskMB: 20 * 1024}
	provisioned, metered, changed := m.normalizeStoredAgentSize(stored)
	if !changed {
		t.Fatal("0.25/256 should normalize onto nano")
	}
	if provisioned.CPULimit != 0.5 || provisioned.MemoryMB != 512 {
		t.Fatalf("provisioned = (%v, %d), want nano (0.5, 512)", provisioned.CPULimit, provisioned.MemoryMB)
	}

	pricing := SandboxPricingConfig{
		Enabled:            true,
		CPUPerHourUSD:      0.10,
		MemoryGBPerHourUSD: 0.04,
		DiskGBPerHourUSD:   0.001,
	}
	start := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	billed, _ := computeSandboxCost(InstanceConfig{
		CPULimit: provisioned.CPULimit,
		MemoryMB: provisioned.MemoryMB,
		DiskMB:   provisioned.DiskMB,
		Metered:  metered,
	}, start, end, pricing, 1.0)
	configured, _ := computeSandboxCost(InstanceConfig{
		CPULimit: stored.CPULimit,
		MemoryMB: stored.MemoryMB,
		DiskMB:   stored.DiskMB,
	}, start, end, pricing, 1.0)
	if math.Abs(billed.TotalUSD-configured.TotalUSD) > 1e-12 {
		t.Fatalf("billed %.6f for the provisioned nano, want %.6f (the configured 0.25/256)", billed.TotalUSD, configured.TotalUSD)
	}
	// 0.25 vCPU * 0.10 + 0.25 GiB * 0.04 + 20 GiB * 0.001
	if want := 0.025 + 0.01 + 0.02; math.Abs(billed.TotalUSD-want) > 1e-12 {
		t.Fatalf("billed %.6f, want %.6f", billed.TotalUSD, want)
	}
}

// TestNormalizedAgentSizeBelowConfiguredIsNotMetered: rounding down (1 vCPU /
// 512 MB -> nano) provisions less than configured, so the provisioned size is
// what is billed and no metered cap is recorded.
func TestNormalizedAgentSizeBelowConfiguredIsNotMetered(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	m.SetManagedMachineProfilesRequired(true)
	provisioned, metered, changed := m.normalizeStoredAgentSize(SandboxConfig{CPULimit: 1, MemoryMB: 512, DiskMB: 20 * 1024})
	if !changed || provisioned.CPULimit != 0.5 || provisioned.MemoryMB != 512 {
		t.Fatalf("provisioned = %+v changed=%v, want nano", provisioned, changed)
	}
	if metered != nil {
		t.Fatalf("metered = %+v, want nil when provisioning does not exceed the configured size", *metered)
	}
}
