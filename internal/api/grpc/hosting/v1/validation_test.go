package v1

import (
	"strings"
	"testing"

	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

func TestPublicReportRequestsEnforceTransportBounds(t *testing.T) {
	tests := []struct {
		name    string
		message interface{ Validate() error }
	}{
		{
			name: "report details",
			message: &hostingpb.ReportSiteRequest{
				Slug: "release-notes", Reason: hostingpb.AbuseReason_ABUSE_REASON_PHISHING,
				Details: strings.Repeat("x", 2001),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.message.Validate(); err == nil {
				t.Fatal("invalid request passed generated validation")
			}
		})
	}
}
