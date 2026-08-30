package transport

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

func TestMapOrgError(t *testing.T) {
	cases := []struct {
		msg  string
		want connect.Code
	}{
		{"insufficient permissions", connect.CodePermissionDenied},
		{"not a member of this organization", connect.CodePermissionDenied},
		{"insufficient permissions to manage workspace members", connect.CodePermissionDenied},
		{"organization not found", connect.CodeNotFound},
		{"target user is not a member", connect.CodeNotFound},
		{"email has already been invited", connect.CodeAlreadyExists},
		{"invitation has expired", connect.CodeFailedPrecondition},
		{"cannot remove yourself", connect.CodeFailedPrecondition},
		{"seat limit reached", connect.CodeResourceExhausted},
		{"invalid organization ID", connect.CodeInvalidArgument},
		{"database connection refused", connect.CodeInternal}, // unclassified -> Internal
	}
	for _, tc := range cases {
		got := connect.CodeOf(mapOrgError(errors.New(tc.msg)))
		if got != tc.want {
			t.Errorf("mapOrgError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
	if mapOrgError(nil) != nil {
		t.Error("mapOrgError(nil) must be nil")
	}
}
