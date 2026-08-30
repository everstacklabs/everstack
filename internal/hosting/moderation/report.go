package moderation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/everstacklabs/everstack/internal/hosting"
)

const (
	maxReportDetailsBytes = 2_000
	maxPagePathBytes      = 2_048
)

var (
	ErrInvalidReport = errors.New("invalid abuse report")
	ErrSiteNotFound  = errors.New("site not found")
)

// Reason is the reporter-selected category. Reports are deliberately
// structured so the operator queue can be filtered without interpreting
// arbitrary prose.
type Reason string

const (
	ReasonPhishing      Reason = "phishing"
	ReasonMalware       Reason = "malware"
	ReasonImpersonation Reason = "impersonation"
	ReasonPrivacy       Reason = "privacy"
	ReasonCopyright     Reason = "copyright"
	ReasonOther         Reason = "other"
)

var validReasons = map[Reason]struct{}{
	ReasonPhishing:      {},
	ReasonMalware:       {},
	ReasonImpersonation: {},
	ReasonPrivacy:       {},
	ReasonCopyright:     {},
	ReasonOther:         {},
}

// Report is the bounded, transport-independent abuse report accepted by the
// hosting control plane. PagePath is site-relative by design; accepting an
// arbitrary URL here would create an unnecessary untrusted-URL surface.
type Report struct {
	Slug       string
	Reason     Reason
	Details    string
	PagePath   string
	ReporterIP string
}

type Receipt struct {
	Accepted bool
}

type ReportStore interface {
	CreateReport(ctx context.Context, report Report) error
}

type Reporter struct {
	store ReportStore
}

func NewReporter(store ReportStore) *Reporter {
	return &Reporter{store: store}
}

// Report validates and records a report. A missing site deliberately returns
// the same accepted receipt as a real site so the public endpoint cannot be
// used to enumerate hosted slugs.
func (r *Reporter) Report(ctx context.Context, report Report) (Receipt, error) {
	report.Slug = strings.ToLower(strings.TrimSpace(report.Slug))
	report.Details = strings.TrimSpace(report.Details)
	report.PagePath = strings.TrimSpace(report.PagePath)

	if err := validateReport(report); err != nil {
		return Receipt{}, err
	}
	if r == nil || r.store == nil {
		return Receipt{}, errors.New("abuse reporting is not configured")
	}
	if err := r.store.CreateReport(ctx, report); err != nil && !errors.Is(err, ErrSiteNotFound) {
		return Receipt{}, err
	}
	return Receipt{Accepted: true}, nil
}

func validateReport(report Report) error {
	if !hosting.ValidSlug(report.Slug) {
		return fmt.Errorf("%w: invalid slug", ErrInvalidReport)
	}
	if _, ok := validReasons[report.Reason]; !ok {
		return fmt.Errorf("%w: invalid reason", ErrInvalidReport)
	}
	if len(report.Details) > maxReportDetailsBytes {
		return fmt.Errorf("%w: details exceed %d bytes", ErrInvalidReport, maxReportDetailsBytes)
	}
	if report.Reason == ReasonOther && report.Details == "" {
		return fmt.Errorf("%w: details are required for reason other", ErrInvalidReport)
	}
	if report.PagePath == "" {
		return nil
	}
	if len(report.PagePath) > maxPagePathBytes || !strings.HasPrefix(report.PagePath, "/") || strings.HasPrefix(report.PagePath, "//") {
		return fmt.Errorf("%w: invalid page path", ErrInvalidReport)
	}
	parsed, err := url.ParseRequestURI(report.PagePath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return fmt.Errorf("%w: invalid page path", ErrInvalidReport)
	}
	return nil
}
