package license_monitor

import (
	"testing"

	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
)

func TestBuildReportUsageRequestCarriesVersionAndActivationLink(t *testing.T) {
	t.Parallel()

	request := buildReportUsageRequest(
		"instance-1",
		"refresh-1",
		"fingerprint-1",
		" v1.8.0 ",
		&licv1.UsageReport{TotalRequests: 12},
	)
	if request.GetInstanceId() != "instance-1" || request.GetRefreshToken() != "refresh-1" {
		t.Fatalf("activation credentials were not preserved: %+v", request)
	}
	if request.GetFingerprint() != "fingerprint-1" {
		t.Fatalf("fingerprint = %q, want activation linkage", request.GetFingerprint())
	}
	if request.GetGatewayVersion() != "v1.8.0" {
		t.Fatalf("gateway version = %q", request.GetGatewayVersion())
	}
}
