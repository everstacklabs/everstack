package sandbox

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

type recordingRootfsBackend struct {
	Backend
	calls    int
	reporter string
	images   []string
	unknown  bool
}

func (b *recordingRootfsBackend) ReportRootfsReferences(_ context.Context, reporter string, images []string, unknown bool) error {
	b.calls++
	b.reporter = reporter
	b.images = images
	b.unknown = unknown
	return nil
}

var rootfsRefsQuery = regexp.QuoteMeta("SELECT DISTINCT image FROM sandbox_instances")

func newRootfsRefsManager(t *testing.T, backend Backend) (*SandboxManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	t.Cleanup(func() { _ = sqlxDB.Close() })
	return &SandboxManager{db: sqlxDB, backend: backend}, mock
}

func TestReportRootfsReferencesSendsLiveImagesAndConfiguredExecutor(t *testing.T) {
	backend := &recordingRootfsBackend{}
	m, mock := newRootfsRefsManager(t, backend)
	mock.ExpectQuery(rootfsRefsQuery).WillReturnRows(sqlmock.NewRows([]string{"image"}).
		AddRow("ghcr.io/everstacklabs/sandbox:node").
		AddRow("everstack/openai-codex-firecracker-rootfs:openai-codex-0.154.0-r3"))

	err := m.reportRootfsReferences(context.Background(), "everstack-gateway/gw-pod-1",
		"everstack/openai-codex-firecracker-rootfs:openai-codex-0.155.0") // configured, unused yet
	if err != nil {
		t.Fatalf("reportRootfsReferences: %v", err)
	}
	if backend.calls != 1 || backend.reporter != "everstack-gateway/gw-pod-1" {
		t.Fatalf("calls=%d reporter=%q", backend.calls, backend.reporter)
	}
	if backend.unknown {
		t.Fatal("report with a configured executor image marked unknown")
	}
	want := []string{
		"everstack/openai-codex-firecracker-rootfs:openai-codex-0.154.0-r3",
		"everstack/openai-codex-firecracker-rootfs:openai-codex-0.155.0",
		"ghcr.io/everstacklabs/sandbox:node",
	}
	if strings.Join(backend.images, "|") != strings.Join(want, "|") {
		t.Fatalf("images = %v\nwant %v", backend.images, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReportRootfsReferencesQueryExcludesOnlyTerminatedRows(t *testing.T) {
	// Sleeping, stopped, archived and failed rows can still wake or be
	// inspected, so their images must stay referenced.
	q := rootfsReferencedImagesQuery
	for _, must := range []string{"NOT IN ('terminated', 'deleted')", "COALESCE(NULLIF(lifecycle_state, ''), status"} {
		if !strings.Contains(q, must) {
			t.Fatalf("query missing %q:\n%s", must, q)
		}
	}
	for _, mustNot := range []string{"sleeping", "archived", "running"} {
		if strings.Contains(q, mustNot) {
			t.Fatalf("query filters on %q; only terminal rows may be excluded:\n%s", mustNot, q)
		}
	}
}

func TestReportRootfsReferencesSkipsReportWhenQueryFails(t *testing.T) {
	// A partial view must never reach the agent: it would let the GC
	// delete an image a sleeping sandbox still needs. Not reporting lets
	// the agent's previous report expire and the GC fail closed.
	backend := &recordingRootfsBackend{}
	m, mock := newRootfsRefsManager(t, backend)
	mock.ExpectQuery(rootfsRefsQuery).WillReturnError(errors.New("db down"))

	if err := m.reportRootfsReferences(context.Background(), "gw", "x:y"); err == nil {
		t.Fatal("expected error")
	}
	if backend.calls != 0 {
		t.Fatalf("reported %d times despite failed query", backend.calls)
	}
}

func TestReportRootfsReferencesNoopForOtherBackends(t *testing.T) {
	m, _ := newRootfsRefsManager(t, nil)
	if err := m.reportRootfsReferences(context.Background(), "gw", "x:y"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestReportRootfsReferencesUnsetExecutorReportsUnknown(t *testing.T) {
	// Without the executor image the gateway cannot name everything the
	// env needs, so it says so instead of sending a set that looks complete.
	backend := &recordingRootfsBackend{}
	m, mock := newRootfsRefsManager(t, backend)
	mock.ExpectQuery(rootfsRefsQuery).WillReturnRows(sqlmock.NewRows([]string{"image"}))

	if err := m.reportRootfsReferences(context.Background(), "everstack-gateway/gw", "  "); err != nil {
		t.Fatalf("err = %v", err)
	}
	if backend.calls != 1 || !backend.unknown {
		t.Fatalf("calls=%d unknown=%v, want one unknown report", backend.calls, backend.unknown)
	}
}

func TestRootfsReporterID(t *testing.T) {
	t.Setenv("EVS_SANDBOX_ROOTFS_REPORTER", "")
	if got := RootfsReporterID("everstack-gateway-7d9f-abcde"); got != "everstack-gateway/everstack-gateway-7d9f-abcde" {
		t.Fatalf("default id = %q", got)
	}
	t.Setenv("EVS_SANDBOX_ROOTFS_REPORTER", "gateway-ssh")
	if got := RootfsReporterID("pod"); got != "gateway-ssh/pod" {
		t.Fatalf("override id = %q", got)
	}
}
