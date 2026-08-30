package moderation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidReportReview = errors.New("invalid abuse report review")

type Overview struct {
	TotalSites     int64 `db:"total_sites"`
	ActiveSites    int64 `db:"active_sites"`
	AnonymousSites int64 `db:"anonymous_sites"`
	ExpiringSites  int64 `db:"expiring_sites"`
	DisabledSites  int64 `db:"disabled_sites"`
	TotalBytes     int64 `db:"total_bytes"`
	OpenReports    int64 `db:"open_reports"`
	PendingActions int64 `db:"pending_actions"`
}

type OperationsReader interface {
	Overview(ctx context.Context) (Overview, error)
	ListSites(ctx context.Context, options ListOptions) (SitePage, error)
	ListReports(ctx context.Context, options ReportListOptions) (ReportPage, error)
	ReviewReport(ctx context.Context, input ReportReview) (AbuseReport, error)
}

type ListOptions struct {
	Search string
	Status string
	Limit  int
	Offset int
}

type OperatorSite struct {
	ID                string     `db:"id"`
	Slug              string     `db:"slug"`
	TenantID          *string    `db:"tenant_id"`
	OwnerUserID       *string    `db:"owner_user_id"`
	Status            string     `db:"status"`
	Access            string     `db:"access"`
	SPAFallback       bool       `db:"spa_fallback"`
	CurrentVersion    *int32     `db:"current_version"`
	TotalBytes        int64      `db:"total_bytes"`
	FileCount         int32      `db:"file_count"`
	KillSwitch        bool       `db:"kill_switch"`
	TakedownReason    string     `db:"takedown_reason"`
	ExpiresAt         *time.Time `db:"expires_at"`
	CreatedAt         time.Time  `db:"created_at"`
	LastPublishedAt   *time.Time `db:"last_published_at"`
	OpenReportCount   int32      `db:"open_report_count"`
	EnforcementStatus string     `db:"enforcement_status"`
}

type SitePage struct {
	Sites      []OperatorSite
	NextOffset int
	HasMore    bool
}

type ReportListOptions struct {
	Status string
	Search string
	Limit  int
	Offset int
}

type AbuseReport struct {
	ID         string     `db:"id"`
	Slug       string     `db:"slug"`
	Reason     Reason     `db:"reason"`
	Details    string     `db:"details"`
	PagePath   string     `db:"page_path"`
	Status     string     `db:"status"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	ResolvedAt *time.Time `db:"resolved_at"`
}

type ReportPage struct {
	Reports    []AbuseReport
	NextOffset int
	HasMore    bool
}

type ReportReview struct {
	ReportID string
	Status   string
	Note     string
	ActorID  string
}

func (r ReportReview) Validate() error {
	if strings.TrimSpace(r.ReportID) == "" || strings.TrimSpace(r.ActorID) == "" {
		return fmt.Errorf("%w: report and actor are required", ErrInvalidReportReview)
	}
	if r.Status != "resolved" && r.Status != "dismissed" {
		return fmt.Errorf("%w: status must be resolved or dismissed", ErrInvalidReportReview)
	}
	if len(r.Note) > maxActionTextBytes {
		return fmt.Errorf("%w: note exceeds %d bytes", ErrInvalidReportReview, maxActionTextBytes)
	}
	return nil
}
