package license_monitor

import (
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestIsPermanentError_NilError(t *testing.T) {
	assert.False(t, isPermanentError(nil))
}

func TestIsPermanentError_LicenseReleased(t *testing.T) {
	err := fmt.Errorf("license has been released")
	assert.True(t, isPermanentError(err))
}

func TestIsPermanentError_PermissionDeniedReleased(t *testing.T) {
	err := connect.NewError(connect.CodePermissionDenied, fmt.Errorf("license has been released"))
	assert.True(t, isPermanentError(err))
}

func TestIsPermanentError_Unauthenticated(t *testing.T) {
	err := connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid token"))
	assert.True(t, isPermanentError(err))
}

func TestIsPermanentError_TransientError(t *testing.T) {
	err := fmt.Errorf("connection timeout")
	assert.False(t, isPermanentError(err))
}

func TestIsPermanentError_PermissionDeniedNotReleased(t *testing.T) {
	// PermissionDenied without "released" in the message is not permanent
	err := connect.NewError(connect.CodePermissionDenied, fmt.Errorf("quota exceeded"))
	assert.False(t, isPermanentError(err))
}

func TestIsPermanentError_InternalError(t *testing.T) {
	err := connect.NewError(connect.CodeInternal, fmt.Errorf("database error"))
	assert.False(t, isPermanentError(err))
}

func TestIsPermanentError_UnavailableError(t *testing.T) {
	err := connect.NewError(connect.CodeUnavailable, fmt.Errorf("service unavailable"))
	assert.False(t, isPermanentError(err))
}

func TestArchivePendingReports_NoRedis(t *testing.T) {
	// Create a syncer with nil redis -- should not panic
	syncer := &UsageSyncer{
		redis:          nil,
		pendingReports: make([]pendingReport, 0),
		stopCh:         make(chan struct{}),
	}

	// Add some pending reports to the in-memory queue
	syncer.pendingReports = append(syncer.pendingReports, pendingReport{})

	// This should not panic even with nil redis
	assert.NotPanics(t, func() {
		syncer.archivePendingReports(nil)
	})
}

func TestArchivePendingReports_NoRedis_WithCurrentReport(t *testing.T) {
	syncer := &UsageSyncer{
		redis:          nil,
		pendingReports: make([]pendingReport, 0),
		stopCh:         make(chan struct{}),
	}

	// Pass a non-nil current report -- should still not panic with nil redis
	assert.NotPanics(t, func() {
		syncer.archivePendingReports(nil)
	})
}

func TestClearPendingReports_NoRedis(t *testing.T) {
	syncer := &UsageSyncer{
		redis:          nil,
		pendingReports: make([]pendingReport, 0),
		stopCh:         make(chan struct{}),
	}

	// Should not panic with nil redis
	assert.NotPanics(t, func() {
		syncer.clearPendingReports()
	})
}
