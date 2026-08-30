package v1

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

func (s *Server) ReportSite(ctx context.Context, req *connect.Request[hostingpb.ReportSiteRequest]) (*connect.Response[hostingpb.ReportSiteResponse], error) {
	if s.reporter == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("abuse reporting is not configured"))
	}
	ip := clientIP(s, req)
	if !s.reportLimiter.Allow(ip) || !s.globalReport.Allow() {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("report rate limit exceeded; retry later"))
	}

	receipt, err := s.reporter.Report(ctx, moderation.Report{
		Slug:       req.Msg.GetSlug(),
		Reason:     abuseReasonFromProto(req.Msg.GetReason()),
		Details:    req.Msg.GetDetails(),
		PagePath:   req.Msg.GetPagePath(),
		ReporterIP: ip,
	})
	if err != nil {
		if errors.Is(err, moderation.ErrInvalidReport) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to record report: %w", err))
	}
	return connect.NewResponse(&hostingpb.ReportSiteResponse{Accepted: receipt.Accepted}), nil
}

func abuseReasonFromProto(reason hostingpb.AbuseReason) moderation.Reason {
	switch reason {
	case hostingpb.AbuseReason_ABUSE_REASON_PHISHING:
		return moderation.ReasonPhishing
	case hostingpb.AbuseReason_ABUSE_REASON_MALWARE:
		return moderation.ReasonMalware
	case hostingpb.AbuseReason_ABUSE_REASON_IMPERSONATION:
		return moderation.ReasonImpersonation
	case hostingpb.AbuseReason_ABUSE_REASON_PRIVACY:
		return moderation.ReasonPrivacy
	case hostingpb.AbuseReason_ABUSE_REASON_COPYRIGHT:
		return moderation.ReasonCopyright
	case hostingpb.AbuseReason_ABUSE_REASON_OTHER:
		return moderation.ReasonOther
	default:
		return ""
	}
}

func (s *Server) GetHostingCapabilities(
	_ context.Context,
	_ *connect.Request[hostingpb.HostingCapabilitiesRequest],
) (*connect.Response[hostingpb.HostingCapabilitiesResponse], error) {
	baseDomain := s.cfg.BaseDomain
	if baseDomain == "" {
		baseDomain = "evs.run"
	}
	return connect.NewResponse(&hostingpb.HostingCapabilitiesResponse{
		HostingEnabled:             s.db != nil && s.store != nil,
		CanOperate:                 false,
		AnonymousPublishingEnabled: s.cfg.AllowAnonymous,
		EdgeEnforcementConfigured:  s.edgeConfigured,
		BaseDomain:                 baseDomain,
	}), nil
}
