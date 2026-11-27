// Package planlimit maps plan cap check results to the Connect errors clients
// see.
package planlimit

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ConnectError maps an error from an enterprise cap check.
//
// A plan cap refusal becomes ResourceExhausted with the customer-facing
// message and an ErrorInfo detail (reason enterprise.PlanLimitReason) that the
// UI uses to render an upgrade path. Anything else (a failed count query, a
// lock timeout) is an internal failure: it is logged and replaced with a
// generic message, because it wraps raw driver text that must not reach users.
// resource names what was being checked, e.g. "agent".
func ConnectError(err error, resource string) *connect.Error {
	if err == nil {
		return nil
	}
	var limitErr *enterprise.LimitError
	if errors.As(err, &limitErr) {
		cErr := connect.NewError(connect.CodeResourceExhausted, limitErr)
		if detail, dErr := connect.NewErrorDetail(limitErr.ErrorInfo()); dErr == nil {
			cErr.AddDetail(detail)
		}
		return cErr
	}
	if errors.Is(err, enterprise.ErrResourceLimit) {
		return connect.NewError(connect.CodeResourceExhausted, err)
	}
	if errors.Is(err, enterprise.ErrLimitCheckUnavailable) {
		// Retryable: the organization-wide count could not be taken, and a
		// partial count must never decide a create.
		logger.WithError(err).Warn("plan limit: organization scope unavailable; create refused")
		return connect.NewError(connect.CodeUnavailable, enterprise.ErrLimitCheckUnavailable)
	}
	logger.WithError(err).Errorf("plan limit: %s limit check failed", resource)
	return connect.NewError(connect.CodeInternal, fmt.Errorf("could not check the %s limit; try again", resource))
}
