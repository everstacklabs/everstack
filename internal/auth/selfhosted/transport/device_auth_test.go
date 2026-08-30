package transport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/google/uuid"
)

type exchangeResultStore struct {
	err error
}

func TestBindDeviceIdentityToVerifiedRequestInstance(t *testing.T) {
	identity := deviceauth.Identity{OrganizationID: "org-1"}
	ctx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1",
	})
	if err := bindDeviceIdentityToRequest(ctx, &identity); err != nil {
		t.Fatalf("bindDeviceIdentityToRequest() error = %v", err)
	}
	if identity.InstanceID != "instance-1" {
		t.Fatalf("InstanceID = %q, want instance-1", identity.InstanceID)
	}

	identity = deviceauth.Identity{OrganizationID: "org-2"}
	if err := bindDeviceIdentityToRequest(ctx, &identity); err == nil {
		t.Fatal("bindDeviceIdentityToRequest() accepted a different organization")
	}
}

func (s *exchangeResultStore) Create(context.Context, string, string, time.Duration) (*deviceauth.Session, error) {
	return nil, errors.New("not implemented")
}

func (s *exchangeResultStore) Redeem(context.Context, string, string, func(*deviceauth.Session) error) error {
	return s.err
}

func (s *exchangeResultStore) GetByUserCode(context.Context, string) (*deviceauth.Session, error) {
	return nil, errors.New("not implemented")
}

func (s *exchangeResultStore) Approve(context.Context, string, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (s *exchangeResultStore) Deny(context.Context, string) error {
	return errors.New("not implemented")
}

func (s *exchangeResultStore) CleanupExpired(context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

func TestExchangeDeviceCodeMapsTerminalAndPollingStates(t *testing.T) {
	t.Parallel()

	tokenManager, err := deviceauth.NewTokenManager([]byte(strings.Repeat("k", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	tests := []struct {
		name           string
		exchangeErr    error
		wantStatus     string
		wantOAuthError string
	}{
		{name: "pending", exchangeErr: deviceauth.ErrAuthorizationPending, wantStatus: "authorization_pending", wantOAuthError: "authorization_pending"},
		{name: "slow down", exchangeErr: deviceauth.ErrSlowDown, wantStatus: "authorization_pending", wantOAuthError: "slow_down"},
		{name: "denied", exchangeErr: deviceauth.ErrAuthorizationDenied, wantStatus: "denied", wantOAuthError: "access_denied"},
		{name: "expired", exchangeErr: deviceauth.ErrSessionExpired, wantStatus: "expired", wantOAuthError: "expired_token"},
		{name: "consumed", exchangeErr: deviceauth.ErrSessionConsumed, wantStatus: "expired", wantOAuthError: "expired_token"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &SelfHostedAuthHandler{
				deviceAuth:    &exchangeResultStore{err: test.exchangeErr},
				organizations: &service.OrganizationService{},
				deviceTokens:  tokenManager,
			}
			response, err := handler.ExchangeDeviceCode(
				context.Background(),
				connect.NewRequest(&authv1.ExchangeDeviceCodeRequest{
					DeviceCode: "device-code",
					ClientId:   "evs-cli",
				}),
			)
			if err != nil {
				t.Fatalf("ExchangeDeviceCode() error = %v", err)
			}
			if got := response.Msg.GetStatus(); got != test.wantStatus {
				t.Fatalf("ExchangeDeviceCode().Status = %q, want %q", got, test.wantStatus)
			}
			if got := response.Msg.GetOauthError(); got != test.wantOAuthError {
				t.Fatalf("ExchangeDeviceCode().OauthError = %q, want %q", got, test.wantOAuthError)
			}
		})
	}
}
