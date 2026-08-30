package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1/authconnect"
	cliv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1/cliconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1/functionsconnect"
)

const defaultTimeout = 30 * time.Second

// Options configure the client factory.
type Options struct {
	// APIURL is the base URL of the Everstack API (e.g. https://auth.everstack.ai).
	APIURL string
	// AccessToken is a Bearer JWT from device-auth.
	AccessToken string
	// AccessTokenSource returns a current bearer token before each request.
	// It is used by OAuth logins to refresh and persist short-lived tokens.
	AccessTokenSource AccessTokenSource
	// APIKey is a raw API key sent as x-evs-api-key.
	// One of AccessToken or APIKey must be set (unless used for unauthenticated calls like device-auth).
	APIKey string
	// OrgID is the organization ID injected as x-evs-org-id (optional, for multi-org users).
	OrgID string
	// TenantID is the tenant ID injected as x-evs-tenant-id (optional).
	TenantID string
	// Timeout is the default per-call timeout. Defaults to 30s.
	Timeout time.Duration
	// Debug enables verbose request logging to stderr.
	Debug bool
}

// AccessTokenSource provides a current bearer token for an API request.
type AccessTokenSource interface {
	AccessToken(context.Context) (string, error)
}

// WhoamiResult is a simple identity summary returned by Whoami.
type WhoamiResult struct {
	Email   string
	UserID  string
	OrgSlug string
	OrgID   string
	OrgName string
}

// Factory creates typed Connect clients pre-wired with auth.
type Factory struct {
	opts       Options
	httpClient *http.Client
}

// New creates a Factory from the given options.
func New(opts Options) *Factory {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	return &Factory{
		opts:       opts,
		httpClient: &http.Client{Timeout: opts.Timeout},
	}
}

// Auth returns a Connect client for the AuthService.
func (f *Factory) Auth() authconnect.AuthServiceClient {
	return authconnect.NewAuthServiceClient(f.httpClient, f.opts.APIURL, f.connectOptions()...)
}

// CLI returns a Connect client for the CLIService.
func (f *Factory) CLI() cliconnect.CLIServiceClient {
	return cliconnect.NewCLIServiceClient(f.httpClient, f.opts.APIURL, f.connectOptions()...)
}

// HTTPClient returns an authenticated client for REST, streaming, and
// WebSocket-adjacent endpoints that do not use generated Connect clients.
func (f *Factory) HTTPClient() *http.Client {
	return &http.Client{
		Timeout: f.httpClient.Timeout,
		Transport: &authTransport{
			base: http.DefaultTransport,
			opts: f.opts,
		},
	}
}

type authTransport struct {
	base http.RoundTripper
	opts Options
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()

	accessToken := t.opts.AccessToken
	if t.opts.AccessTokenSource != nil {
		var err error
		accessToken, err = t.opts.AccessTokenSource.AccessToken(req.Context())
		if err != nil {
			return nil, err
		}
	}
	if accessToken != "" {
		cloned.Header.Set("Authorization", "Bearer "+accessToken)
	} else if t.opts.APIKey != "" {
		cloned.Header.Set("x-evs-api-key", t.opts.APIKey)
	}
	if t.opts.OrgID != "" {
		cloned.Header.Set("x-evs-org-id", t.opts.OrgID)
	}
	if t.opts.TenantID != "" {
		cloned.Header.Set("x-evs-tenant-id", t.opts.TenantID)
	}
	return t.base.RoundTrip(cloned)
}

// connectOptions returns the standard set of Connect client options for this factory.
func (f *Factory) connectOptions() []connect.ClientOption {
	return []connect.ClientOption{
		connect.WithInterceptors(f.authInterceptor()),
	}
}

// authInterceptor injects authentication headers on every outbound call.
// Bearer JWT (device-auth) goes into Authorization header.
// Raw API key goes into the canonical x-evs-api-key header (the server also
// accepts the legacy x-mf-api-key from older CLIs).
func (f *Factory) authInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			h := req.Header()
			accessToken := f.opts.AccessToken
			if f.opts.AccessTokenSource != nil {
				var err error
				accessToken, err = f.opts.AccessTokenSource.AccessToken(ctx)
				if err != nil {
					return nil, err
				}
			}
			if accessToken != "" {
				h.Set("Authorization", "Bearer "+accessToken)
			} else if f.opts.APIKey != "" {
				h.Set("x-evs-api-key", f.opts.APIKey)
			}
			if f.opts.OrgID != "" {
				h.Set("x-evs-org-id", f.opts.OrgID)
			}
			if f.opts.TenantID != "" {
				h.Set("x-evs-tenant-id", f.opts.TenantID)
			}
			return next(ctx, req)
		}
	}
}

// Agents returns a Connect client for the AgentsService.
func (f *Factory) Agents() agentsconnect.AgentsServiceClient {
	return agentsconnect.NewAgentsServiceClient(f.httpClient, f.opts.APIURL, f.connectOptions()...)
}

// AgentsStreaming returns an authenticated AgentsService client without a
// request timeout so RunTurnStream can remain open for long-running turns.
func (f *Factory) AgentsStreaming() agentsconnect.AgentsServiceClient {
	return agentsconnect.NewAgentsServiceClient(&http.Client{
		Transport: &authTransport{
			base: http.DefaultTransport,
			opts: f.opts,
		},
	}, f.opts.APIURL, f.connectOptions()...)
}

// Functions returns a Connect client for the FunctionsService.
func (f *Factory) Functions() functionsconnect.FunctionsServiceClient {
	return functionsconnect.NewFunctionsServiceClient(f.httpClient, f.opts.APIURL, f.connectOptions()...)
}

// Whoami calls the CLI identity endpoint, which supports both API keys and
// device-auth Bearer tokens.
func (f *Factory) Whoami(ctx context.Context) (*WhoamiResult, error) {
	resp, err := f.CLI().Whoami(ctx, connect.NewRequest(&cliv1.WhoamiRequest{}))
	if err != nil {
		return nil, MapError(err)
	}
	identity := resp.Msg
	if identity.GetUserId() == "" {
		return nil, fmt.Errorf("server returned an empty session")
	}
	return &WhoamiResult{
		UserID:  identity.GetUserId(),
		Email:   identity.GetEmail(),
		OrgID:   identity.GetOrgId(),
		OrgSlug: identity.GetOrgSlug(),
	}, nil
}

// MapError converts a Connect/gRPC error into a human-readable CLI error.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		code := connectErr.Code()
		msg := connectErr.Message()
		switch code {
		case connect.CodeUnauthenticated:
			return fmt.Errorf("not authenticated - run `evs login` first")
		case connect.CodePermissionDenied:
			return fmt.Errorf("permission denied: %s", msg)
		case connect.CodeNotFound:
			return fmt.Errorf("not found: %s", msg)
		case connect.CodeAlreadyExists:
			return fmt.Errorf("already exists: %s", msg)
		case connect.CodeInvalidArgument:
			return fmt.Errorf("invalid argument: %s", msg)
		case connect.CodeResourceExhausted:
			return fmt.Errorf("rate limited: %s", msg)
		case connect.CodeUnavailable:
			return fmt.Errorf("server unavailable - check your API URL or try again")
		default:
			if msg != "" {
				return fmt.Errorf("%s", msg)
			}
			return fmt.Errorf("API error (%s)", strings.ToLower(code.String()))
		}
	}
	return err
}
