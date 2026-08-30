package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/alerts"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	alertspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/alerts/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/alerts/v1/alertsconnect"
	"github.com/google/uuid"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// requireTenantID extracts the tenant id from context. Every alerts
// RPC was reading req.Msg.TenantId directly — Pattern A from the
// 2026-05-06 P0. This helper enforces the post-fix contract: tenant
// id from context only, PermissionDenied when missing.
func requireTenantID(ctx context.Context) (string, error) {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid, nil
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// Server implements the AlertsService gRPC server.
type Server struct {
	ctx                 context.Context
	store               alerts.AlertStore
	evaluator           *alerts.Evaluator
	serviceInterceptors []connect.Interceptor
}

// CreateServer creates a new alerts Server.
func CreateServer(ctx context.Context, store alerts.AlertStore, evaluator *alerts.Evaluator) *Server {
	return &Server{ctx: ctx, store: store, evaluator: evaluator}
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *Server) WithInterceptors(interceptors ...connect.Interceptor) *Server {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return alertsconnect.NewAlertsServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return alertspb.File_everstack_alerts_v1_alerts_service_proto
}

func (s *Server) AppName() string      { return alertsconnect.AlertsServiceName }
func (s *Server) MethodPrefix() string { return alertsconnect.AlertsServiceName }

func (s *Server) RegisterGateway(_ context.Context, _ *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	return nil
}

// ─── AlertRule CRUD ──────────────────────────────────────────────────

func (s *Server) CreateAlertRule(ctx context.Context, req *connect.Request[alertspb.CreateAlertRuleRequest]) (*connect.Response[alertspb.CreateAlertRuleResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg

	filtersJSON := []byte("{}")
	if msg.Filters != nil {
		b, err := json.Marshal(msg.Filters.AsMap())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid filters: %w", err))
		}
		filtersJSON = b
	}

	record := &alerts.AlertRuleRecord{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		Name:            msg.Name,
		Description:     msg.Description,
		Category:        categoryToString(msg.Category),
		Severity:        severityToString(msg.Severity),
		Metric:          msg.Metric,
		Operator:        operatorToString(msg.Operator),
		Threshold:       msg.Threshold,
		DurationSeconds: msg.DurationSeconds,
		Filters:         filtersJSON,
		Enabled:         msg.Enabled,
	}

	if err := s.store.CreateAlertRule(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create alert rule: %w", err))
	}

	if len(msg.TargetIds) > 0 {
		if err := s.store.SetRuleTargets(ctx, record.ID, msg.TargetIds); err != nil {
			logger.WithError(err).Warn("alerts: failed to set rule targets")
		}
	}

	logger.WithFields("rule", record.Name).Info("alerts: created alert rule")
	return connect.NewResponse(&alertspb.CreateAlertRuleResponse{Rule: ruleRecordToProto(record, msg.TargetIds)}), nil
}

func (s *Server) GetAlertRule(ctx context.Context, req *connect.Request[alertspb.GetAlertRuleRequest]) (*connect.Response[alertspb.GetAlertRuleResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	record, err := s.store.GetAlertRule(ctx, req.Msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("alert rule not found"))
	}
	targetIDs, _ := s.store.GetRuleTargetIDs(ctx, record.ID)
	return connect.NewResponse(&alertspb.GetAlertRuleResponse{Rule: ruleRecordToProto(record, targetIDs)}), nil
}

func (s *Server) UpdateAlertRule(ctx context.Context, req *connect.Request[alertspb.UpdateAlertRuleRequest]) (*connect.Response[alertspb.UpdateAlertRuleResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	existing, err := s.store.GetAlertRule(ctx, msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("alert rule not found"))
	}

	if msg.Name != nil {
		existing.Name = *msg.Name
	}
	if msg.Description != nil {
		existing.Description = *msg.Description
	}
	if msg.Category != nil {
		existing.Category = categoryToString(*msg.Category)
	}
	if msg.Severity != nil {
		existing.Severity = severityToString(*msg.Severity)
	}
	if msg.Metric != nil {
		existing.Metric = *msg.Metric
	}
	if msg.Operator != nil {
		existing.Operator = operatorToString(*msg.Operator)
	}
	if msg.Threshold != nil {
		existing.Threshold = *msg.Threshold
	}
	if msg.DurationSeconds != nil {
		existing.DurationSeconds = *msg.DurationSeconds
	}
	if msg.Filters != nil {
		b, _ := json.Marshal(msg.Filters.AsMap())
		existing.Filters = b
	}
	if msg.Enabled != nil {
		existing.Enabled = *msg.Enabled
	}
	if msg.MutedUntil != nil {
		existing.MutedUntil = sql.NullTime{Time: msg.MutedUntil.AsTime(), Valid: true}
	}

	if err := s.store.UpdateAlertRule(ctx, existing); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if len(msg.TargetIds) > 0 {
		if err := s.store.SetRuleTargets(ctx, existing.ID, msg.TargetIds); err != nil {
			logger.WithError(err).Warn("alerts: failed to update rule targets")
		}
	}

	targetIDs, _ := s.store.GetRuleTargetIDs(ctx, existing.ID)
	return connect.NewResponse(&alertspb.UpdateAlertRuleResponse{Rule: ruleRecordToProto(existing, targetIDs)}), nil
}

func (s *Server) DeleteAlertRule(ctx context.Context, req *connect.Request[alertspb.DeleteAlertRuleRequest]) (*connect.Response[alertspb.DeleteAlertRuleResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteAlertRule(ctx, req.Msg.Id, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&alertspb.DeleteAlertRuleResponse{}), nil
}

func (s *Server) ListAlertRules(ctx context.Context, req *connect.Request[alertspb.ListAlertRulesRequest]) (*connect.Response[alertspb.ListAlertRulesResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	var category *string
	if msg.Category != nil && *msg.Category != alertspb.AlertCategory_ALERT_CATEGORY_UNSPECIFIED {
		c := categoryToString(*msg.Category)
		category = &c
	}
	var enabled *bool
	if msg.Enabled != nil {
		enabled = msg.Enabled
	}

	records, total, err := s.store.ListAlertRules(ctx, tenantID, category, enabled, msg.Limit, msg.Offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	rules := make([]*alertspb.AlertRule, 0, len(records))
	for _, r := range records {
		targetIDs, _ := s.store.GetRuleTargetIDs(ctx, r.ID)
		rules = append(rules, ruleRecordToProto(r, targetIDs))
	}
	return connect.NewResponse(&alertspb.ListAlertRulesResponse{Rules: rules, Total: total}), nil
}

// ─── NotificationTarget CRUD ─────────────────────────────────────────

func (s *Server) CreateNotificationTarget(ctx context.Context, req *connect.Request[alertspb.CreateNotificationTargetRequest]) (*connect.Response[alertspb.CreateNotificationTargetResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg

	webhookHeaders := []byte("{}")
	if msg.WebhookHeaders != nil {
		b, _ := json.Marshal(msg.WebhookHeaders.AsMap())
		webhookHeaders = b
	}

	record := &alerts.NotificationTargetRecord{
		ID:                 uuid.New().String(),
		TenantID:           tenantID,
		Name:               msg.Name,
		TargetType:         targetTypeToString(msg.TargetType),
		ChannelConfigID:    optionalStringToNullString(msg.ChannelConfigId),
		PlatformChannelRef: optionalStringToNullString(msg.PlatformChannelRef),
		WebhookURL:         optionalStringToNullString(msg.WebhookUrl),
		WebhookHeaders:     webhookHeaders,
		EmailAddresses:     nonNilStrings(msg.EmailAddresses),
		MinSeverity:        severityToString(msg.MinSeverity),
		Enabled:            msg.Enabled,
	}

	if err := s.store.CreateNotificationTarget(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create notification target: %w", err))
	}

	logger.WithFields("target", record.Name).Info("alerts: created notification target")
	return connect.NewResponse(&alertspb.CreateNotificationTargetResponse{Target: targetRecordToProto(record)}), nil
}

func (s *Server) GetNotificationTarget(ctx context.Context, req *connect.Request[alertspb.GetNotificationTargetRequest]) (*connect.Response[alertspb.GetNotificationTargetResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	record, err := s.store.GetNotificationTarget(ctx, req.Msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("notification target not found"))
	}
	return connect.NewResponse(&alertspb.GetNotificationTargetResponse{Target: targetRecordToProto(record)}), nil
}

func (s *Server) UpdateNotificationTarget(ctx context.Context, req *connect.Request[alertspb.UpdateNotificationTargetRequest]) (*connect.Response[alertspb.UpdateNotificationTargetResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	existing, err := s.store.GetNotificationTarget(ctx, msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("notification target not found"))
	}

	if msg.Name != nil {
		existing.Name = *msg.Name
	}
	if msg.TargetType != nil {
		existing.TargetType = targetTypeToString(*msg.TargetType)
	}
	if msg.ChannelConfigId != nil {
		existing.ChannelConfigID = optionalStringToNullString(msg.ChannelConfigId)
	}
	if msg.PlatformChannelRef != nil {
		existing.PlatformChannelRef = optionalStringToNullString(msg.PlatformChannelRef)
	}
	if msg.WebhookUrl != nil {
		existing.WebhookURL = optionalStringToNullString(msg.WebhookUrl)
	}
	if msg.WebhookHeaders != nil {
		b, _ := json.Marshal(msg.WebhookHeaders.AsMap())
		existing.WebhookHeaders = b
	}
	if len(msg.EmailAddresses) > 0 {
		existing.EmailAddresses = msg.EmailAddresses
	}
	if msg.MinSeverity != nil {
		existing.MinSeverity = severityToString(*msg.MinSeverity)
	}
	if msg.Enabled != nil {
		existing.Enabled = *msg.Enabled
	}

	if err := s.store.UpdateNotificationTarget(ctx, existing); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&alertspb.UpdateNotificationTargetResponse{Target: targetRecordToProto(existing)}), nil
}

func (s *Server) DeleteNotificationTarget(ctx context.Context, req *connect.Request[alertspb.DeleteNotificationTargetRequest]) (*connect.Response[alertspb.DeleteNotificationTargetResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteNotificationTarget(ctx, req.Msg.Id, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&alertspb.DeleteNotificationTargetResponse{}), nil
}

func (s *Server) ListNotificationTargets(ctx context.Context, req *connect.Request[alertspb.ListNotificationTargetsRequest]) (*connect.Response[alertspb.ListNotificationTargetsResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	var targetType *string
	if msg.TargetType != nil && *msg.TargetType != alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_UNSPECIFIED {
		t := targetTypeToString(*msg.TargetType)
		targetType = &t
	}

	records, total, err := s.store.ListNotificationTargets(ctx, tenantID, targetType, msg.Limit, msg.Offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	targets := make([]*alertspb.NotificationTarget, 0, len(records))
	for _, r := range records {
		targets = append(targets, targetRecordToProto(r))
	}
	return connect.NewResponse(&alertspb.ListNotificationTargetsResponse{Targets: targets, Total: total}), nil
}

func (s *Server) TestNotificationTarget(ctx context.Context, req *connect.Request[alertspb.TestNotificationTargetRequest]) (*connect.Response[alertspb.TestNotificationTargetResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.store.GetNotificationTarget(ctx, req.Msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if target == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("notification target not found"))
	}

	if s.evaluator != nil {
		if err := s.evaluator.TestNotificationTarget(ctx, target); err != nil {
			return connect.NewResponse(&alertspb.TestNotificationTargetResponse{
				Success: false,
				Message: err.Error(),
			}), nil
		}
	}

	return connect.NewResponse(&alertspb.TestNotificationTargetResponse{
		Success: true,
		Message: "Test notification sent successfully",
	}), nil
}

// ─── AlertEvent Operations ───────────────────────────────────────────

func (s *Server) ListAlertEvents(ctx context.Context, req *connect.Request[alertspb.ListAlertEventsRequest]) (*connect.Response[alertspb.ListAlertEventsResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	var ruleID *string
	if msg.AlertRuleId != nil && *msg.AlertRuleId != "" {
		ruleID = msg.AlertRuleId
	}
	var status *string
	if msg.Status != nil && *msg.Status != alertspb.AlertEventStatus_ALERT_EVENT_STATUS_UNSPECIFIED {
		st := eventStatusToString(*msg.Status)
		status = &st
	}

	records, total, err := s.store.ListAlertEvents(ctx, tenantID, ruleID, status, msg.Limit, msg.Offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	events := make([]*alertspb.AlertEvent, 0, len(records))
	for _, r := range records {
		events = append(events, eventRecordToProto(r))
	}
	return connect.NewResponse(&alertspb.ListAlertEventsResponse{Events: events, Total: total}), nil
}

func (s *Server) AcknowledgeAlert(ctx context.Context, req *connect.Request[alertspb.AcknowledgeAlertRequest]) (*connect.Response[alertspb.AcknowledgeAlertResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	// Simplified: find and acknowledge the event among the latest 250
	// events for this tenant. The earlier dead-code branch with
	// `_ = events / _ = query / _ = event` was an abandoned scaffold.
	allEvents, _, err := s.store.ListAlertEvents(ctx, tenantID, nil, nil, 250, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var found *alerts.AlertEventWithRule
	for _, e := range allEvents {
		if e.ID == req.Msg.Id {
			found = e
			break
		}
	}
	if found == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("alert event not found"))
	}

	now := sql.NullTime{Time: timestamppb.Now().AsTime(), Valid: true}
	found.AcknowledgedAt = now
	found.AcknowledgedBy = sql.NullString{String: req.Msg.AcknowledgedBy, Valid: true}
	found.Status = "acknowledged"

	if err := s.store.UpdateAlertEvent(ctx, &found.AlertEventRecord); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&alertspb.AcknowledgeAlertResponse{Event: eventRecordToProto(found)}), nil
}

func (s *Server) ResolveAlert(ctx context.Context, req *connect.Request[alertspb.ResolveAlertRequest]) (*connect.Response[alertspb.ResolveAlertResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	allEvents, _, err := s.store.ListAlertEvents(ctx, tenantID, nil, nil, 250, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var found *alerts.AlertEventWithRule
	for _, e := range allEvents {
		if e.ID == req.Msg.Id {
			found = e
			break
		}
	}
	if found == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("alert event not found"))
	}

	now := sql.NullTime{Time: timestamppb.Now().AsTime(), Valid: true}
	found.ResolvedAt = now
	found.Status = "resolved"

	if err := s.store.UpdateAlertEvent(ctx, &found.AlertEventRecord); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&alertspb.ResolveAlertResponse{Event: eventRecordToProto(found)}), nil
}

// ─── Builtin + Summary ──────────────────────────────────────────────

func (s *Server) SeedBuiltinRules(ctx context.Context, req *connect.Request[alertspb.SeedBuiltinRulesRequest]) (*connect.Response[alertspb.SeedBuiltinRulesResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	count, err := alerts.SeedBuiltinRules(ctx, s.store, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("seed builtin rules: %w", err))
	}
	logger.WithFields("count", count, "tenant", tenantID).Info("alerts: seeded builtin rules")
	return connect.NewResponse(&alertspb.SeedBuiltinRulesResponse{SeededCount: count}), nil
}

func (s *Server) GetAlertsSummary(ctx context.Context, req *connect.Request[alertspb.GetAlertsSummaryRequest]) (*connect.Response[alertspb.GetAlertsSummaryResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := s.store.GetAlertsSummary(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&alertspb.GetAlertsSummaryResponse{
		Summary: &alertspb.AlertsSummary{
			FiringCount:       summary.FiringCount,
			AcknowledgedCount: summary.AcknowledgedCount,
			TotalRules:        summary.TotalRules,
			EnabledRules:      summary.EnabledRules,
		},
	}), nil
}

// ─── Helpers ─────────────────────────────────────────────────────────

func ruleRecordToProto(r *alerts.AlertRuleRecord, targetIDs []string) *alertspb.AlertRule {
	rule := &alertspb.AlertRule{
		Id:              r.ID,
		TenantId:        r.TenantID,
		Name:            r.Name,
		Description:     r.Description,
		Category:        stringToCategory(r.Category),
		Severity:        stringToSeverity(r.Severity),
		Metric:          r.Metric,
		Operator:        stringToOperator(r.Operator),
		Threshold:       r.Threshold,
		DurationSeconds: r.DurationSeconds,
		Enabled:         r.Enabled,
		TargetIds:       targetIDs,
		CreatedAt:       timestamppb.New(r.CreatedAt),
		UpdatedAt:       timestamppb.New(r.UpdatedAt),
	}
	if r.BuiltinKey.Valid {
		rule.BuiltinKey = &r.BuiltinKey.String
	}
	if r.MutedUntil.Valid {
		rule.MutedUntil = timestamppb.New(r.MutedUntil.Time)
	}
	if len(r.Filters) > 0 {
		var fMap map[string]interface{}
		if json.Unmarshal(r.Filters, &fMap) == nil {
			if s, err := structpb.NewStruct(fMap); err == nil {
				rule.Filters = s
			}
		}
	}
	return rule
}

func targetRecordToProto(r *alerts.NotificationTargetRecord) *alertspb.NotificationTarget {
	target := &alertspb.NotificationTarget{
		Id:             r.ID,
		TenantId:       r.TenantID,
		Name:           r.Name,
		TargetType:     stringToTargetType(r.TargetType),
		EmailAddresses: r.EmailAddresses,
		MinSeverity:    stringToSeverity(r.MinSeverity),
		Enabled:        r.Enabled,
		CreatedAt:      timestamppb.New(r.CreatedAt),
		UpdatedAt:      timestamppb.New(r.UpdatedAt),
	}
	if r.ChannelConfigID.Valid {
		target.ChannelConfigId = &r.ChannelConfigID.String
	}
	if r.PlatformChannelRef.Valid {
		target.PlatformChannelRef = &r.PlatformChannelRef.String
	}
	if r.WebhookURL.Valid {
		target.WebhookUrl = &r.WebhookURL.String
	}
	if len(r.WebhookHeaders) > 0 {
		var hMap map[string]interface{}
		if json.Unmarshal(r.WebhookHeaders, &hMap) == nil {
			if s, err := structpb.NewStruct(hMap); err == nil {
				target.WebhookHeaders = s
			}
		}
	}
	return target
}

func eventRecordToProto(r *alerts.AlertEventWithRule) *alertspb.AlertEvent {
	event := &alertspb.AlertEvent{
		Id:                r.ID,
		TenantId:          r.TenantID,
		AlertRuleId:       r.AlertRuleID,
		AlertRuleName:     r.AlertRuleName,
		Severity:          stringToSeverity(r.Severity),
		Status:            stringToEventStatus(r.Status),
		MetricValue:       r.MetricValue,
		Threshold:         r.Threshold,
		FiredAt:           timestamppb.New(r.FiredAt),
		NotificationCount: r.NotificationCount,
	}
	if r.AcknowledgedAt.Valid {
		event.AcknowledgedAt = timestamppb.New(r.AcknowledgedAt.Time)
	}
	if r.AcknowledgedBy.Valid {
		event.AcknowledgedBy = &r.AcknowledgedBy.String
	}
	if r.ResolvedAt.Valid {
		event.ResolvedAt = timestamppb.New(r.ResolvedAt.Time)
	}
	if r.LastNotifiedAt.Valid {
		event.LastNotifiedAt = timestamppb.New(r.LastNotifiedAt.Time)
	}
	return event
}

func categoryToString(c alertspb.AlertCategory) string {
	switch c {
	case alertspb.AlertCategory_ALERT_CATEGORY_PERFORMANCE:
		return "performance"
	case alertspb.AlertCategory_ALERT_CATEGORY_COST:
		return "cost"
	case alertspb.AlertCategory_ALERT_CATEGORY_PROVIDER:
		return "provider"
	case alertspb.AlertCategory_ALERT_CATEGORY_CUSTOM:
		return "custom"
	default:
		return "custom"
	}
}

func stringToCategory(s string) alertspb.AlertCategory {
	switch s {
	case "performance":
		return alertspb.AlertCategory_ALERT_CATEGORY_PERFORMANCE
	case "cost":
		return alertspb.AlertCategory_ALERT_CATEGORY_COST
	case "provider":
		return alertspb.AlertCategory_ALERT_CATEGORY_PROVIDER
	case "custom":
		return alertspb.AlertCategory_ALERT_CATEGORY_CUSTOM
	default:
		return alertspb.AlertCategory_ALERT_CATEGORY_UNSPECIFIED
	}
}

func severityToString(s alertspb.AlertSeverity) string {
	switch s {
	case alertspb.AlertSeverity_ALERT_SEVERITY_CRITICAL:
		return "critical"
	case alertspb.AlertSeverity_ALERT_SEVERITY_WARNING:
		return "warning"
	case alertspb.AlertSeverity_ALERT_SEVERITY_INFO:
		return "info"
	default:
		return "warning"
	}
}

func stringToSeverity(s string) alertspb.AlertSeverity {
	switch s {
	case "critical":
		return alertspb.AlertSeverity_ALERT_SEVERITY_CRITICAL
	case "warning":
		return alertspb.AlertSeverity_ALERT_SEVERITY_WARNING
	case "info":
		return alertspb.AlertSeverity_ALERT_SEVERITY_INFO
	default:
		return alertspb.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED
	}
}

func operatorToString(o alertspb.ComparisonOperator) string {
	switch o {
	case alertspb.ComparisonOperator_COMPARISON_OPERATOR_GT:
		return ">"
	case alertspb.ComparisonOperator_COMPARISON_OPERATOR_LT:
		return "<"
	case alertspb.ComparisonOperator_COMPARISON_OPERATOR_GTE:
		return ">="
	case alertspb.ComparisonOperator_COMPARISON_OPERATOR_LTE:
		return "<="
	default:
		return ">"
	}
}

func stringToOperator(s string) alertspb.ComparisonOperator {
	switch s {
	case ">":
		return alertspb.ComparisonOperator_COMPARISON_OPERATOR_GT
	case "<":
		return alertspb.ComparisonOperator_COMPARISON_OPERATOR_LT
	case ">=":
		return alertspb.ComparisonOperator_COMPARISON_OPERATOR_GTE
	case "<=":
		return alertspb.ComparisonOperator_COMPARISON_OPERATOR_LTE
	default:
		return alertspb.ComparisonOperator_COMPARISON_OPERATOR_UNSPECIFIED
	}
}

func targetTypeToString(t alertspb.NotificationTargetType) string {
	switch t {
	case alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_CHANNEL:
		return "channel"
	case alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_WEBHOOK:
		return "webhook"
	case alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_EMAIL:
		return "email"
	default:
		return "channel"
	}
}

func stringToTargetType(s string) alertspb.NotificationTargetType {
	switch s {
	case "channel":
		return alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_CHANNEL
	case "webhook":
		return alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_WEBHOOK
	case "email":
		return alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_EMAIL
	default:
		return alertspb.NotificationTargetType_NOTIFICATION_TARGET_TYPE_UNSPECIFIED
	}
}

func eventStatusToString(s alertspb.AlertEventStatus) string {
	switch s {
	case alertspb.AlertEventStatus_ALERT_EVENT_STATUS_FIRING:
		return "firing"
	case alertspb.AlertEventStatus_ALERT_EVENT_STATUS_ACKNOWLEDGED:
		return "acknowledged"
	case alertspb.AlertEventStatus_ALERT_EVENT_STATUS_RESOLVED:
		return "resolved"
	default:
		return "firing"
	}
}

func stringToEventStatus(s string) alertspb.AlertEventStatus {
	switch s {
	case "firing":
		return alertspb.AlertEventStatus_ALERT_EVENT_STATUS_FIRING
	case "acknowledged":
		return alertspb.AlertEventStatus_ALERT_EVENT_STATUS_ACKNOWLEDGED
	case "resolved":
		return alertspb.AlertEventStatus_ALERT_EVENT_STATUS_RESOLVED
	default:
		return alertspb.AlertEventStatus_ALERT_EVENT_STATUS_UNSPECIFIED
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func optionalStringToNullString(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}
