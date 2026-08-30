package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

type apiReportStore struct {
	reports []moderation.Report
}

func (s *apiReportStore) CreateReport(_ context.Context, report moderation.Report) error {
	s.reports = append(s.reports, report)
	return nil
}

func TestReportSiteAcceptsAValidPublicReport(t *testing.T) {
	store := &apiReportStore{}
	server := CreateServerWithDeps(context.Background(), nil, nil, Config{ProxyToken: "worker-secret"})
	server.SetReporter(moderation.NewReporter(store))

	req := connect.NewRequest(&hostingpb.ReportSiteRequest{
		Slug:     "release-notes",
		Reason:   hostingpb.AbuseReason_ABUSE_REASON_PHISHING,
		Details:  "The page asks for an Everstack password.",
		PagePath: "/login",
	})
	req.Header().Set("X-EVS-Client-IP", "203.0.113.20")
	req.Header().Set("X-EVS-Proxy-Token", "worker-secret")

	resp, err := server.ReportSite(context.Background(), req)
	if err != nil {
		t.Fatalf("ReportSite: %v", err)
	}
	if !resp.Msg.GetAccepted() {
		t.Fatal("report response was not accepted")
	}
	if len(store.reports) != 1 {
		t.Fatalf("stored reports = %d, want 1", len(store.reports))
	}
	if got := store.reports[0].ReporterIP; got != "203.0.113.20" {
		t.Fatalf("reporter IP = %q, want trusted Cloudflare IP", got)
	}
}

func TestReportSiteDoesNotPersistAnInvalidForwardedIP(t *testing.T) {
	store := &apiReportStore{}
	server := CreateServerWithDeps(context.Background(), nil, nil, Config{ProxyToken: "worker-secret"})
	server.SetReporter(moderation.NewReporter(store))
	req := connect.NewRequest(&hostingpb.ReportSiteRequest{
		Slug: "release-notes", Reason: hostingpb.AbuseReason_ABUSE_REASON_MALWARE,
	})
	req.Header().Set("X-EVS-Client-IP", "not-an-ip")
	req.Header().Set("X-EVS-Proxy-Token", "worker-secret")

	if _, err := server.ReportSite(context.Background(), req); err != nil {
		t.Fatalf("ReportSite: %v", err)
	}
	if got := store.reports[0].ReporterIP; got != "" {
		t.Fatalf("stored reporter IP = %q, want empty", got)
	}
}

func TestReportSiteDoesNotTrustASpoofedCloudflareHeader(t *testing.T) {
	store := &apiReportStore{}
	server := CreateServerWithDeps(context.Background(), nil, nil, Config{ProxyToken: "worker-secret"})
	server.SetReporter(moderation.NewReporter(store))
	req := connect.NewRequest(&hostingpb.ReportSiteRequest{
		Slug: "release-notes", Reason: hostingpb.AbuseReason_ABUSE_REASON_MALWARE,
	})
	req.Header().Set("CF-Connecting-IP", "203.0.113.99")
	req.Header().Set("X-EVS-Client-IP", "203.0.113.99")
	req.Header().Set("X-EVS-Proxy-Token", "wrong-secret")

	if _, err := server.ReportSite(context.Background(), req); err != nil {
		t.Fatalf("ReportSite: %v", err)
	}
	if got := store.reports[0].ReporterIP; got == "203.0.113.99" {
		t.Fatalf("stored spoofed reporter IP %q", got)
	}
}
