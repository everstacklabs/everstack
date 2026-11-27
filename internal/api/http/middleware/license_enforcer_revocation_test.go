package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/cqrs"
	licpkg "github.com/everstacklabs/everstack/internal/license"
	querypkg "github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/query/handlers/license"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
)

// credsBus answers GetInstanceCredentials the way the gateway DB would for an
// activated instance.
type credsBus struct{}

func (credsBus) Execute(context.Context, querypkg.Query) (interface{}, error) {
	return &license.InstanceCredentials{InstanceID: "inst-1", RefreshToken: "rt"}, nil
}
func (credsBus) RegisterHandler(querypkg.QueryHandler) {}

type refreshRefuser struct {
	licenseconnect.UnimplementedInstanceServiceHandler
	err error
}

func (r refreshRefuser) RefreshInstance(context.Context, *connect.Request[licv1.RefreshInstanceRequest]) (*connect.Response[licv1.RefreshInstanceResponse], error) {
	return nil, r.err
}

// licensedEnforcer is a paid instance mid-term, talking to a license service
// that answers every refresh with err.
func licensedEnforcer(t *testing.T, err error) *LicenseEnforcer {
	t.Helper()
	_, h := licenseconnect.NewInstanceServiceHandler(refreshRefuser{err: err})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	l := NewLicenseEnforcer(&cqrs.System{QueryBus: credsBus{}})
	l.licenseServiceURL = srv.URL
	l.SetCachedState(&LicenseState{Active: true, Status: "active", Tier: "enterprise", IsPaid: true})
	l.mu.Lock()
	l.cachedJWT = "jwt"
	l.cachedClaims = &licpkg.Claims{Tier: "enterprise"}
	l.mu.Unlock()
	return l
}

// A revoked instance (license released or moved to another machine) drops to
// Community Edition on its next refresh, instead of keeping its cached tier
// until the cached expiry. It is CE, not the write-blocking "revoked" posture.
func TestRefreshOfRevokedInstanceDegradesToCE(t *testing.T) {
	l := licensedEnforcer(t, connect.NewError(connect.CodePermissionDenied, errors.New("instance has been revoked")))
	if err := l.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	st := l.GetCached()
	if st == nil || st.Active || st.IsPaid || st.Tier != "free" || st.Status == "revoked" {
		t.Fatalf("state after revocation = %+v, want inactive free CE", st)
	}
	l.mu.RLock()
	jwt, claims := l.cachedJWT, l.cachedClaims
	l.mu.RUnlock()
	if jwt != "" || claims != nil {
		t.Fatalf("revoked license JWT still cached (jwt=%q claims=%v)", jwt, claims)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		code, ran, status := serve(t, l, method)
		if !ran || code != http.StatusOK || status != "unlicensed" {
			t.Fatalf("%s after revocation: code=%d ran=%v status=%q, want CE pass-through", method, code, ran, status)
		}
	}
}

// A temporary database failure must not leave a revoked JWT on disk forever
// merely because the first refusal already cleared the in-memory copy.
func TestRevocationRetriesClearingStoredJWT(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	l := licensedEnforcer(t, connect.NewError(connect.CodePermissionDenied, errors.New("instance has been revoked")))
	l.SetDB(sqlx.NewDb(rawDB, "postgres"))

	for attempt := 0; attempt < 2; attempt++ {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT signed_payload FROM system.instances")).
			WillReturnRows(sqlmock.NewRows([]string{"signed_payload"}).AddRow(`{"license_jwt":"jwt","license_public_key":"key"}`))
		write := mock.ExpectExec(regexp.QuoteMeta("UPDATE system.instances SET signed_payload = $1")).
			WithArgs([]byte(`{"license_public_key":"key"}`))
		if attempt == 0 {
			write.WillReturnError(errors.New("temporary database outage"))
		} else {
			write.WillReturnResult(sqlmock.NewResult(0, 1))
		}
		if err := l.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("revocation refresh %d: %v", attempt+1, err)
		}
	}
}

// Any other refusal, including an unrelated PermissionDenied from a
// misconfigured service, keeps the cached license: only an explicit
// revocation may take a paying customer's license away.
func TestRefreshFailureOtherThanRevocationKeepsLicense(t *testing.T) {
	for name, err := range map[string]error{
		"permission denied, not revoked": connect.NewError(connect.CodePermissionDenied, errors.New("caller lacks license:refresh")),
		"unauthenticated":                connect.NewError(connect.CodeUnauthenticated, errors.New("invalid refresh token")),
		"unavailable":                    connect.NewError(connect.CodeUnavailable, errors.New("instance has been revoked")),
	} {
		t.Run(name, func(t *testing.T) {
			l := licensedEnforcer(t, err)
			if err := l.refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if st := l.GetCached(); st == nil || !st.Active || st.Tier != "enterprise" {
				t.Fatalf("state = %+v, want the cached enterprise license kept", st)
			}
			l.mu.RLock()
			defer l.mu.RUnlock()
			if l.cachedClaims == nil {
				t.Fatal("cached claims dropped on a non-revocation failure")
			}
		})
	}
}
