package deviceauth

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type deviceCodeDigestArgument struct{}

func (deviceCodeDigestArgument) Match(value driver.Value) bool {
	digest, ok := value.(string)
	return ok && strings.HasPrefix(digest, "sha256:") && len(digest) == len("sha256:")+64
}

func TestRepositoryCreateStoresDeviceCodeDigest(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO device_authorization_sessions")).
		WithArgs(
			sqlmock.AnyArg(),
			deviceCodeDigestArgument{},
			sqlmock.AnyArg(),
			"evs-cli",
			"cli:full",
			StatusPending,
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "last_polled_at"}).
			AddRow(time.Now().UTC(), time.Now().UTC()))

	repository := NewRepository(sqlx.NewDb(db, "sqlmock"))
	session, err := repository.Create(context.Background(), "evs-cli", "cli:full", 15*time.Minute)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if session.DeviceCode == "" {
		t.Fatal("Create() returned an empty device code")
	}
	if strings.HasPrefix(session.DeviceCode, "sha256:") {
		t.Fatal("Create() returned the stored digest instead of the bearer device code")
	}
	if session.LastPolledAt == nil {
		t.Fatal("Create() did not start the server-side polling interval")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}

func TestRepositoryRedeemRequiresMatchingClient(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rawDeviceCode := "raw-device-code"
	now := time.Now().UTC()
	columns := []string{
		"id", "user_code", "client_id", "scope", "status", "user_id", "org_id",
		"expires_at", "created_at", "last_polled_at", "poll_interval_seconds",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			uuid.New(), "ABCD-EFGH", "evs-cli", "cli:full", StatusAuthorized,
			uuid.New(), uuid.New(), now.Add(10*time.Minute), now.Add(-time.Minute), nil, 5,
		))
	mock.ExpectRollback()

	repository := NewRepository(sqlx.NewDb(db, "sqlmock"))
	if err := repository.Redeem(context.Background(), rawDeviceCode, "", func(*Session) error {
		t.Fatal("issuer called for a mismatched client")
		return nil
	}); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("Redeem() error = %v, want ErrClientMismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}

func TestRepositoryExchangeConsumesAuthorizedSessionOnce(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rawDeviceCode := "raw-device-code"
	sessionID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	createdAt := time.Now().UTC().Add(-time.Minute)
	columns := []string{
		"id", "user_code", "client_id", "scope", "status", "user_id", "org_id",
		"expires_at", "created_at", "last_polled_at", "poll_interval_seconds",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			sessionID, "ABCD-EFGH", "evs-cli", "cli:full", StatusAuthorized,
			userID, orgID, expiresAt, createdAt, nil, 5,
		))
	mock.ExpectExec("(?s)UPDATE device_authorization_sessions.*status = 'consumed'").
		WithArgs(sqlmock.AnyArg(), sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			sessionID, "ABCD-EFGH", "evs-cli", "cli:full", StatusConsumed,
			userID, orgID, expiresAt, createdAt, time.Now().UTC(), 5,
		))
	mock.ExpectRollback()

	repository := NewRepository(sqlx.NewDb(db, "sqlmock"))
	issued := 0
	err = repository.Redeem(context.Background(), rawDeviceCode, "evs-cli", func(session *Session) error {
		issued++
		if session.ID != sessionID {
			t.Fatalf("Redeem() session ID = %s, want %s", session.ID, sessionID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("first Redeem() error = %v", err)
	}

	if err := repository.Redeem(context.Background(), rawDeviceCode, "evs-cli", func(*Session) error {
		issued++
		return nil
	}); !errors.Is(err, ErrSessionConsumed) {
		t.Fatalf("second Redeem() error = %v, want ErrSessionConsumed", err)
	}
	if issued != 1 {
		t.Fatalf("issuer called %d times, want exactly once", issued)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}

func TestRepositoryExchangeRateLimitsPendingPolls(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rawDeviceCode := "raw-device-code"
	now := time.Now().UTC()
	columns := []string{
		"id", "user_code", "client_id", "scope", "status", "user_id", "org_id",
		"expires_at", "created_at", "last_polled_at", "poll_interval_seconds",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			uuid.New(), "ABCD-EFGH", "evs-cli", "cli:full", StatusPending,
			nil, nil, now.Add(10*time.Minute), now.Add(-time.Minute), now, 5,
		))
	mock.ExpectRollback()

	repository := NewRepository(sqlx.NewDb(db, "sqlmock"))
	if err := repository.Redeem(context.Background(), rawDeviceCode, "evs-cli", func(*Session) error {
		t.Fatal("issuer called for a pending session")
		return nil
	}); !errors.Is(err, ErrSlowDown) {
		t.Fatalf("Redeem() error = %v, want ErrSlowDown", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}

func TestRepositoryExchangeRecordsAcceptedPendingPoll(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rawDeviceCode := "raw-device-code"
	now := time.Now().UTC()
	sessionID := uuid.New()
	columns := []string{
		"id", "user_code", "client_id", "scope", "status", "user_id", "org_id",
		"expires_at", "created_at", "last_polled_at", "poll_interval_seconds",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			sessionID, "ABCD-EFGH", "evs-cli", "cli:full", StatusPending,
			nil, nil, now.Add(10*time.Minute), now.Add(-time.Minute), nil, 5,
		))
	mock.ExpectExec("(?s)UPDATE device_authorization_sessions.*last_polled_at = \\$1").
		WithArgs(sqlmock.AnyArg(), sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repository := NewRepository(sqlx.NewDb(db, "sqlmock"))
	if err := repository.Redeem(context.Background(), rawDeviceCode, "evs-cli", func(*Session) error {
		t.Fatal("issuer called for a pending session")
		return nil
	}); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("Redeem() error = %v, want ErrAuthorizationPending", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}

func TestRepositoryRedeemRollsBackWhenIssuerFails(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rawDeviceCode := "raw-device-code"
	sessionID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()
	now := time.Now().UTC()
	columns := []string{
		"id", "user_code", "client_id", "scope", "status", "user_id", "org_id",
		"expires_at", "created_at", "last_polled_at", "poll_interval_seconds",
	}
	authorizedRow := func() *sqlmock.Rows {
		return sqlmock.NewRows(columns).AddRow(
			sessionID, "ABCD-EFGH", "evs-cli", "cli:full", StatusAuthorized,
			userID, orgID, now.Add(10*time.Minute), now.Add(-time.Minute), nil, 5,
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(authorizedRow())
	mock.ExpectRollback()

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT .* FROM device_authorization_sessions.*FOR UPDATE").
		WithArgs(deviceCodeDigest(rawDeviceCode), rawDeviceCode).
		WillReturnRows(authorizedRow())
	mock.ExpectExec("(?s)UPDATE device_authorization_sessions.*status = 'consumed'").
		WithArgs(sqlmock.AnyArg(), sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repository := NewRepository(sqlx.NewDb(db, "sqlmock"))
	issuerFailure := errors.New("temporary signer failure")
	if err := repository.Redeem(context.Background(), rawDeviceCode, "evs-cli", func(*Session) error {
		return issuerFailure
	}); !errors.Is(err, issuerFailure) {
		t.Fatalf("first Redeem() error = %v, want issuer failure", err)
	}
	if err := repository.Redeem(context.Background(), rawDeviceCode, "evs-cli", func(*Session) error {
		return nil
	}); err != nil {
		t.Fatalf("retry Redeem() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}
