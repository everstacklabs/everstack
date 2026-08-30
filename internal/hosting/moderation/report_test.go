package moderation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
)

type recordingReportStore struct {
	reports []moderation.Report
	err     error
}

func (s *recordingReportStore) CreateReport(_ context.Context, report moderation.Report) error {
	s.reports = append(s.reports, report)
	return s.err
}

func TestReporterAcceptsAValidReport(t *testing.T) {
	store := &recordingReportStore{}
	reporter := moderation.NewReporter(store)

	receipt, err := reporter.Report(context.Background(), moderation.Report{
		Slug:       "release-notes",
		Reason:     moderation.ReasonPhishing,
		Details:    "The page asks visitors to enter their Everstack password.",
		PagePath:   "/login",
		ReporterIP: "203.0.113.10",
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !receipt.Accepted {
		t.Fatal("valid report was not accepted")
	}
	if len(store.reports) != 1 {
		t.Fatalf("stored reports = %d, want 1", len(store.reports))
	}
	if got := store.reports[0].Reason; got != moderation.ReasonPhishing {
		t.Fatalf("stored reason = %q, want %q", got, moderation.ReasonPhishing)
	}
}

func TestReporterDoesNotRevealAnUnknownSite(t *testing.T) {
	store := &recordingReportStore{err: moderation.ErrSiteNotFound}
	reporter := moderation.NewReporter(store)

	receipt, err := reporter.Report(context.Background(), moderation.Report{
		Slug:       "missing-site",
		Reason:     moderation.ReasonOther,
		Details:    "Suspicious content.",
		ReporterIP: "203.0.113.11",
	})
	if err != nil {
		t.Fatalf("unknown-site report returned an observable error: %v", err)
	}
	if !receipt.Accepted {
		t.Fatal("unknown-site report returned a different receipt")
	}
}

func TestReporterRejectsInvalidInputBeforeStorage(t *testing.T) {
	store := &recordingReportStore{}
	reporter := moderation.NewReporter(store)

	tests := []struct {
		name   string
		report moderation.Report
	}{
		{
			name: "reserved slug",
			report: moderation.Report{
				Slug: "login", Reason: moderation.ReasonPhishing, ReporterIP: "203.0.113.12",
			},
		},
		{
			name: "unknown reason",
			report: moderation.Report{
				Slug: "valid-site", Reason: moderation.Reason("made-up"), ReporterIP: "203.0.113.12",
			},
		},
		{
			name: "absolute page URL",
			report: moderation.Report{
				Slug: "valid-site", Reason: moderation.ReasonOther, PagePath: "https://example.com/", ReporterIP: "203.0.113.12",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := reporter.Report(context.Background(), tt.report); !errors.Is(err, moderation.ErrInvalidReport) {
				t.Fatalf("error = %v, want ErrInvalidReport", err)
			}
		})
	}
	if len(store.reports) != 0 {
		t.Fatalf("invalid reports reached storage: %d", len(store.reports))
	}
}
