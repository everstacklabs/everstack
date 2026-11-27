package billingmeter

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		return -1
	}
	return m.GetCounter().GetValue()
}

// downDB is a platform DB that refuses every connection: the "arbiter
// unavailable" case without a real outage.
func downDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("pgx", "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func wouldDeny(surface, reason, model string) float64 {
	return counterValue(admissionWouldDeny.WithLabelValues(surface, reason, model))
}

func evaluated(surface, outcome string) float64 {
	return counterValue(admissionEvaluated.WithLabelValues(surface, outcome))
}

func TestShadowAdmitPlatformDBDownReportsUnavailable(t *testing.T) {
	m := &Meter{billingDB: downDB(t), shadow: newShadowRunner(4, 3*time.Second)}
	before := wouldDeny(SurfaceInference, ReasonArbiterUnavailable, "db-down-model")
	start := time.Now()
	m.ShadowAdmit(AdmissionRequest{OrgID: "11111111-1111-1111-1111-111111111111", Model: "db-down-model", EstimateMicros: 10})
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("ShadowAdmit blocked the caller for %v", elapsed)
	}
	m.shadow.wait()
	if got := wouldDeny(SurfaceInference, ReasonArbiterUnavailable, "db-down-model") - before; got != 1 {
		t.Fatalf("arbiter_unavailable would-deny delta = %v, want 1", got)
	}
}

func TestShadowAdmitOrgResolutionErrorIsUnavailable(t *testing.T) {
	m := &Meter{billingDB: downDB(t), shadow: newShadowRunner(4, time.Second)}
	before := wouldDeny(SurfaceInference, ReasonArbiterUnavailable, "resolve-err-model")
	m.ShadowAdmit(AdmissionRequest{ResolveErr: errors.New("platform db timeout"), Model: "resolve-err-model"})
	if got := wouldDeny(SurfaceInference, ReasonArbiterUnavailable, "resolve-err-model") - before; got != 1 {
		t.Fatalf("delta = %v, want 1 (evaluated inline, no DB needed)", got)
	}
}

func TestShadowAdmitInternalIsExemptAndInflightIsInline(t *testing.T) {
	m := &Meter{billingDB: downDB(t), shadow: newShadowRunner(4, time.Second)}
	exempt := evaluated(SurfaceInference, "exempt")
	m.ShadowAdmit(AdmissionRequest{OrgID: "o", Internal: true, Model: "x"})
	if got := evaluated(SurfaceInference, "exempt") - exempt; got != 1 {
		t.Fatalf("exempt delta = %v, want 1", got)
	}
	before := wouldDeny(SurfaceInference, ReasonInflightLimit, "inflight-model")
	m.ShadowAdmit(AdmissionRequest{OrgID: "o", Model: "inflight-model", Inflight: 5, InflightCap: 4})
	if got := wouldDeny(SurfaceInference, ReasonInflightLimit, "inflight-model") - before; got != 1 {
		t.Fatalf("inflight delta = %v, want 1", got)
	}
}

func TestShadowAdmitNoOrgAndInactiveMeterAreSkipped(t *testing.T) {
	skipped := evaluated(SurfaceInference, "skipped_no_org")
	m := &Meter{billingDB: downDB(t), shadow: newShadowRunner(4, time.Second)}
	m.ShadowAdmit(AdmissionRequest{Model: "x"})
	if got := evaluated(SurfaceInference, "skipped_no_org") - skipped; got != 1 {
		t.Fatalf("skipped delta = %v, want 1", got)
	}
	var nilMeter *Meter
	nilMeter.ShadowAdmit(AdmissionRequest{OrgID: "o"}) // must not panic
	(&Meter{}).ShadowAdmit(AdmissionRequest{OrgID: "o"})
}

func TestShadowRunnerDropsWhenSaturated(t *testing.T) {
	r := newShadowRunner(1, time.Second)
	release := make(chan struct{})
	if !r.try(func(context.Context) { <-release }) {
		t.Fatal("first evaluation should take the only slot")
	}
	if r.try(func(context.Context) {}) {
		t.Fatal("a saturated runner must drop, not queue or block")
	}
	close(release)
	r.wait()
	if !r.try(func(context.Context) {}) {
		t.Fatal("slot not returned after the evaluation finished")
	}
	r.wait()

	m := &Meter{billingDB: downDB(t), shadow: newShadowRunner(1, time.Second)}
	block := make(chan struct{})
	m.shadow.try(func(context.Context) { <-block })
	dropped := counterValue(admissionShadowDropped.WithLabelValues(SurfaceInference))
	m.ShadowAdmit(AdmissionRequest{OrgID: "o", Model: "x"})
	if got := counterValue(admissionShadowDropped.WithLabelValues(SurfaceInference)) - dropped; got != 1 {
		t.Fatalf("dropped delta = %v, want 1", got)
	}
	close(block)
	m.shadow.wait()
}

func TestShadowAdmitComputeReportsUnavailableWithoutTouchingCaller(t *testing.T) {
	before := wouldDeny(SurfaceSandboxCreate, ReasonArbiterUnavailable, "compute")
	start := time.Now()
	ShadowAdmitComputeTenant(downDB(t), "tenant-db-down", SurfaceSandboxCreate)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("ShadowAdmitComputeTenant blocked the caller for %v", elapsed)
	}
	computeShadow.wait()
	if got := wouldDeny(SurfaceSandboxCreate, ReasonArbiterUnavailable, "compute") - before; got != 1 {
		t.Fatalf("compute unavailable delta = %v, want 1", got)
	}
	ShadowAdmitComputeTenant(nil, "tenant", SurfaceSandboxCreate) // no DB: no-op
	ShadowAdmitComputeTenant(downDB(t), " ", SurfaceSandboxCreate)
}

func TestShadowRunnerRecoversPanics(t *testing.T) {
	r := newShadowRunner(1, time.Second)
	before := counterValue(admissionShadowPanics)
	if !r.try(func(context.Context) { panic("boom") }) {
		t.Fatal("slot expected")
	}
	r.wait()
	if got := counterValue(admissionShadowPanics) - before; got != 1 {
		t.Fatalf("panics delta = %v, want 1", got)
	}
	if !r.try(func(context.Context) {}) {
		t.Fatal("slot not returned after a panic")
	}
	r.wait()
}

func TestLogLimiterSuppressesAndReportsCount(t *testing.T) {
	l := &logLimiter{interval: time.Minute}
	t0 := time.Unix(0, 0)
	if _, ok := l.allow("k", t0); !ok {
		t.Fatal("first line must emit")
	}
	for i := 1; i <= 3; i++ {
		if _, ok := l.allow("k", t0.Add(time.Duration(i)*time.Second)); ok {
			t.Fatal("within the interval must be suppressed")
		}
	}
	if _, ok := l.allow("other", t0.Add(time.Second)); !ok {
		t.Fatal("keys are independent")
	}
	n, ok := l.allow("k", t0.Add(2*time.Minute))
	if !ok || n != 3 {
		t.Fatalf("after the interval: emit=%v suppressed=%d, want true, 3", ok, n)
	}
}
