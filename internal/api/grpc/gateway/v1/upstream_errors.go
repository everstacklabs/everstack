package v1

import (
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// providerErrorToConnect gives a provider failure a status the caller can act
// on. Without it an upstream 4xx left the handler as an untyped error, which
// Connect reports as Unknown and grpc-gateway renders as HTTP 500 with the
// provider's raw JSON body as the message.
//
// Upstream 401/403 deliberately map to FailedPrecondition, not
// Unauthenticated/PermissionDenied: the rejected credential is the
// configured provider key (tenant BYOK or platform-managed, so the message
// stays neutral about who must fix it), not the caller's gateway key, and
// the dashboard treats PermissionDenied as a reason to log the user out.
func providerErrorToConnect(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return err
	}
	var cfgErr *gw.ProviderConfigError
	if errors.As(err, &cfgErr) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	upstream, ok := gw.AsUpstreamError(err)
	if !ok {
		return err
	}
	switch s := upstream.StatusCode; {
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the upstream provider (%s) rejected the configured API key: %w", upstream.Provider, err))
	case s == http.StatusNotFound:
		return connect.NewError(connect.CodeNotFound, err)
	case s == http.StatusRequestTimeout:
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case s == http.StatusTooManyRequests:
		return connect.NewError(connect.CodeResourceExhausted, err)
	case s >= 500:
		return connect.NewError(connect.CodeUnavailable, err)
	case s >= 400:
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeUnknown, err)
	}
}

// keyRotationError reports a request that failed on its original key and
// then again after rotating to another configured key. Returning only the
// last error hid the actionable one: on dev the first key's 401
// invalid_api_key was replaced by the second key's opaque 400. The first
// error stays first in the chain, so classification follows it.
func keyRotationError(first, last error) error {
	if last == nil || last == first || (first != nil && last.Error() == first.Error()) {
		return first
	}
	if first == nil {
		return last
	}
	return fmt.Errorf("%w; after rotating to another configured key: %w", first, last)
}

// connectToGRPCError carries a Connect status across the classic gRPC server,
// which only understands errors with a GRPCStatus method. The error details
// travel too: grpc-gateway renders them on /v1, and the spend limit and model
// trial refusals are recognised there by their google.rpc.ErrorInfo reason
// (middleware.SpendLimitStatus answers 402, or 429 with Retry-After).
func connectToGRPCError(err error) error {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err
	}
	st := &spb.Status{Code: int32(connectErr.Code()), Message: connectErr.Message()}
	for _, d := range connectErr.Details() {
		st.Details = append(st.Details, &anypb.Any{TypeUrl: "type.googleapis.com/" + d.Type(), Value: d.Bytes()})
	}
	return status.ErrorProto(st)
}
