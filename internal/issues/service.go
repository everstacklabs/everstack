package issues

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jmoiron/sqlx"
)

// Derived status returned to callers. "regressed" is computed, not stored.
const StatusRegressed = "regressed"

// Issue is an issue group with its triage state overlaid.
type Issue struct {
	Fingerprint    string
	Title          string
	Signature      string
	Category       string
	Status         string
	Count          int64
	FirstSeen      time.Time
	LastSeen       time.Time
	Provider       string
	Model          string
	SampleTraceIDs []string
	Assignee       *string
	ResolvedAt     *time.Time
	SnoozeUntil    *time.Time
	Spark          []int32
}

// IssueDetail bundles an issue with its occurrences and trend.
type IssueDetail struct {
	Issue       Issue
	Occurrences []Occurrence
	Trend       []TrendPoint
	LatestEvent *EventDetail
	Breadcrumbs []SpanCrumb
	Tags        []TagDistribution
	Users       int64
	Activity    []IssueActivity
}

// ListParams are the inputs to ListIssues.
type ListParams struct {
	From         time.Time
	To           time.Time
	StatusFilter string // "" = all; one of unresolved/resolved/ignored/regressed
	Query        string
	Limit        int
	Offset       int
}

// Service composes the trace-store aggregation with the Postgres triage state.
type Service struct {
	ch *CHStore
	pg *PGStore
}

func NewService(conn clickhouse.Conn, db *sqlx.DB) *Service {
	return &Service{ch: NewCHStore(conn), pg: NewPGStore(db)}
}

// deriveStatus overlays triage state onto an aggregation, detecting regressions
// (a resolved issue whose latest occurrence is after it was resolved).
func deriveStatus(st *IssueState, lastSeen time.Time) (status string, assignee *string, resolvedAt, snooze *time.Time) {
	if st == nil {
		return StatusUnresolved, nil, nil, nil
	}
	status = st.Status
	if status == StatusResolved && st.ResolvedAt != nil && lastSeen.After(*st.ResolvedAt) {
		status = StatusRegressed
	}
	return status, st.Assignee, st.ResolvedAt, st.SnoozeUntil
}

func mergeIssue(a IssueAgg, st *IssueState) Issue {
	status, assignee, resolvedAt, snooze := deriveStatus(st, a.LastSeen)
	return Issue{
		Fingerprint:    a.Fingerprint,
		Title:          a.Title,
		Signature:      a.Signature,
		Category:       a.Category,
		Status:         status,
		Count:          a.Count,
		FirstSeen:      a.FirstSeen,
		LastSeen:       a.LastSeen,
		Provider:       a.Provider,
		Model:          a.Model,
		SampleTraceIDs: a.SampleTraceIDs,
		Assignee:       assignee,
		ResolvedAt:     resolvedAt,
		SnoozeUntil:    snooze,
		Spark:          a.Spark,
	}
}

// ListIssues returns merged, status-filtered issues ranked by count.
func (s *Service) ListIssues(ctx context.Context, tenantID string, p ListParams) ([]Issue, error) {
	// Over-fetch so a post-merge status filter still yields a full page.
	fetchLimit := p.Limit
	if fetchLimit <= 0 {
		fetchLimit = 100
	}
	if p.StatusFilter != "" {
		fetchLimit = fetchLimit*2 + p.Offset
	}
	aggs, err := s.ch.ListIssues(ctx, tenantID, p.From, p.To, p.Query, fetchLimit, 0)
	if err != nil {
		return nil, err
	}
	fps := make([]string, 0, len(aggs))
	for _, a := range aggs {
		fps = append(fps, a.Fingerprint)
	}
	states, err := s.pg.GetStates(ctx, tenantID, fps)
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, 0, len(aggs))
	for _, a := range aggs {
		var st *IssueState
		if v, ok := states[a.Fingerprint]; ok {
			st = &v
		}
		iss := mergeIssue(a, st)
		if p.StatusFilter != "" && iss.Status != p.StatusFilter {
			continue
		}
		issues = append(issues, iss)
	}
	// Apply offset/limit after filtering (CH already sorted by count desc).
	if p.Offset > 0 && p.Offset < len(issues) {
		issues = issues[p.Offset:]
	} else if p.Offset >= len(issues) {
		issues = nil
	}
	if p.Limit > 0 && len(issues) > p.Limit {
		issues = issues[:p.Limit]
	}
	return issues, nil
}

// GetIssue returns one issue with occurrences and trend.
func (s *Service) GetIssue(ctx context.Context, tenantID, fingerprint string, from, to time.Time, interval string) (*IssueDetail, error) {
	agg, err := s.ch.GetIssue(ctx, tenantID, fingerprint, from, to)
	if err != nil {
		return nil, err
	}
	st, err := s.pg.GetState(ctx, tenantID, fingerprint)
	if err != nil {
		return nil, err
	}
	var iss Issue
	if agg != nil {
		iss = mergeIssue(*agg, st)
	} else if st != nil {
		// Aged out of the window but still tracked: surface the stored state.
		status, assignee, resolvedAt, snooze := deriveStatus(st, time.Time{})
		iss = Issue{Fingerprint: fingerprint, Title: st.Title, Signature: st.Signature,
			Status: status, Assignee: assignee, ResolvedAt: resolvedAt, SnoozeUntil: snooze}
	} else {
		iss = Issue{Fingerprint: fingerprint, Status: StatusUnresolved}
	}

	occ, err := s.ch.Occurrences(ctx, tenantID, fingerprint, from, to, 50)
	if err != nil {
		return nil, err
	}
	trend, err := s.ch.Trend(ctx, tenantID, fingerprint, from, to, interval)
	if err != nil {
		return nil, err
	}

	detail := &IssueDetail{Issue: iss, Occurrences: occ, Trend: trend}

	// Event-centric enrichment. These power the highlights / breadcrumbs /
	// tags / users panels; a failure in any one shouldn't sink the whole
	// detail, so they degrade gracefully rather than erroring the request.
	if ev, e := s.ch.LatestEvent(ctx, tenantID, fingerprint, from, to); e == nil && ev != nil {
		detail.LatestEvent = ev
		if crumbs, e2 := s.ch.Breadcrumbs(ctx, tenantID, ev.TraceID); e2 == nil {
			detail.Breadcrumbs = crumbs
		}
	}
	if tags, e := s.ch.TagDistributions(ctx, tenantID, fingerprint, from, to); e == nil {
		detail.Tags = tags
	}
	if users, e := s.ch.DistinctUsers(ctx, tenantID, fingerprint, from, to); e == nil {
		detail.Users = users
	}
	if acts, e := s.pg.ListActivity(ctx, tenantID, fingerprint, 50); e == nil {
		detail.Activity = acts
	}

	return detail, nil
}

// UpdateStatus persists a triage change and returns the issue as currently seen
// over the last 7 days (so the UI reflects fresh counts after the change).
func (s *Service) UpdateStatus(ctx context.Context, tenantID, actor string, st *IssueState) (*Issue, error) {
	st.TenantID = tenantID

	// Capture the prior state so we can record what actually changed.
	prev, _ := s.pg.GetState(ctx, tenantID, st.Fingerprint)
	fromStatus := StatusUnresolved
	var prevAssignee *string
	if prev != nil {
		fromStatus = prev.Status
		prevAssignee = prev.Assignee
	}

	// A nil assignee means "not changing it" (status-only updates omit it); an
	// explicit empty string means unassign. Preserve the prior assignee in the
	// former case so a resolve/ignore doesn't silently drop ownership.
	if st.Assignee == nil {
		st.Assignee = prevAssignee
	}

	if err := s.pg.UpsertState(ctx, st); err != nil {
		return nil, err
	}

	// Triage history: log a status transition and/or an (un)assignment.
	if actor == "" {
		actor = "system"
	}
	if st.Status != fromStatus {
		action := st.Status
		if st.Status == StatusUnresolved {
			action = "reopened"
		}
		_ = s.pg.InsertActivity(ctx, tenantID, st.Fingerprint, IssueActivity{
			Actor: actor, Action: action, FromStatus: fromStatus, ToStatus: st.Status,
		})
	}
	if !sameStr(prevAssignee, st.Assignee) {
		action, note := "assigned", ""
		if st.Assignee == nil || *st.Assignee == "" {
			action = "unassigned"
		} else {
			note = *st.Assignee
		}
		_ = s.pg.InsertActivity(ctx, tenantID, st.Fingerprint, IssueActivity{
			Actor: actor, Action: action, Note: note,
		})
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -7)
	agg, err := s.ch.GetIssue(ctx, tenantID, st.Fingerprint, from, to)
	if err != nil {
		return nil, err
	}
	stored, err := s.pg.GetState(ctx, tenantID, st.Fingerprint)
	if err != nil {
		return nil, err
	}
	var iss Issue
	if agg != nil {
		iss = mergeIssue(*agg, stored)
	} else {
		status, assignee, resolvedAt, snooze := deriveStatus(stored, time.Time{})
		iss = Issue{Fingerprint: st.Fingerprint, Title: st.Title, Signature: st.Signature,
			Status: status, Assignee: assignee, ResolvedAt: resolvedAt, SnoozeUntil: snooze}
	}
	return &iss, nil
}

// sameStr reports whether two optional strings are equal (nil == nil, nil != "").
func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
