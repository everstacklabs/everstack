package catalog_sync

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/jmoiron/sqlx"
)

const testProjectionVersion = "2.0.0"

var testProjectionDigest = strings.Repeat("a", 64)

type recordingCatalogEventBus struct {
	published    []database.Event
	publishCalls int
	err          error
}

func (b *recordingCatalogEventBus) Publish(_ context.Context, events ...database.Event) error {
	b.publishCalls++
	if b.err != nil {
		return b.err
	}
	b.published = append(b.published, events...)
	return nil
}

type eventTypesArgument []string

func (want eventTypesArgument) Match(value driver.Value) bool {
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return false
	}
	var events []database.Event
	if err := json.Unmarshal(data, &events); err != nil || len(events) != len(want) {
		return false
	}
	for index := range want {
		if events[index].Type != want[index] || events[index].ID == "" {
			return false
		}
	}
	return true
}

func TestDBReconcilerCommitsProjectionAuditEventsAndJournalTogether(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	types := eventTypesArgument{"catalog.provider.added", "ModelAddedFromCatalog"}
	events := fixedJournalEvents(types...)
	expectNewProjection(mock, false, true, types)
	expectPublication(mock, events)

	bus := &recordingCatalogEventBus{}
	reconciler := NewDBReconciler(db, bus)
	if err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, catalogMergeResult(true)); err != nil {
		t.Fatal(err)
	}
	assertEventTypes(t, bus.published, types...)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDBReconcilerRollsBackProjectionWhenAuditPersistenceFails(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE catalog_projection_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT bundle_sha256").WithArgs(testProjectionVersion).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT EXISTS\\(.*organization_id IS NULL").WithArgs("example").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("INSERT INTO provider_configurations").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO events").WillReturnError(errors.New("event table unavailable"))
	mock.ExpectRollback()

	reconciler := NewDBReconciler(db, nil)
	err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, catalogMergeResult(false))
	if err == nil || !strings.Contains(err.Error(), "event table unavailable") {
		t.Fatalf("ReconcileFromCatalog() error = %v, want event persistence failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDBReconcilerRollsBackProjectionWhenJournalCannotCommit(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	types := eventTypesArgument{"catalog.provider.added"}
	expectProjectionBeforeJournal(mock, false, false, types)
	mock.ExpectExec("INSERT INTO catalog_projection_releases").
		WithArgs(testProjectionVersion, testProjectionDigest, types).
		WillReturnError(errors.New("journal unavailable"))
	mock.ExpectRollback()

	reconciler := NewDBReconciler(db, nil)
	err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, catalogMergeResult(false))
	if err == nil || !strings.Contains(err.Error(), "journal unavailable") {
		t.Fatalf("ReconcileFromCatalog() error = %v, want journal failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDBReconcilerRejectsSameVersionWithDifferentBundle(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE catalog_projection_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT bundle_sha256").WithArgs(testProjectionVersion).
		WillReturnRows(sqlmock.NewRows([]string{"bundle_sha256"}).AddRow(strings.Repeat("b", 64)))
	mock.ExpectRollback()

	reconciler := NewDBReconciler(db, nil)
	err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, &MergeResult{})
	if err == nil || !strings.Contains(err.Error(), "conflicting bundle") {
		t.Fatalf("ReconcileFromCatalog() error = %v, want conflicting bundle rejection", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDBReconcilerCompletedJournalDoesNotReplayProjectionOrAuditEvents(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	types := eventTypesArgument{"catalog.provider.added"}
	events := fixedJournalEvents(types...)
	expectNewProjection(mock, false, false, types)
	expectPublication(mock, events)
	expectExistingProjection(mock)
	expectPublicationDone(mock)

	bus := &recordingCatalogEventBus{}
	reconciler := NewDBReconciler(db, bus)
	for attempt := 0; attempt < 2; attempt++ {
		if err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, catalogMergeResult(false)); err != nil {
			t.Fatalf("reconciliation attempt %d: %v", attempt+1, err)
		}
	}
	if bus.publishCalls != 1 || len(bus.published) != 1 {
		t.Fatalf("publication calls/events = (%d, %d), want one", bus.publishCalls, len(bus.published))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDBReconcilerRetriesPublicationFromExactJournalPlan(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	types := eventTypesArgument{"catalog.provider.added"}
	events := fixedJournalEvents(types...)
	expectNewProjection(mock, false, false, types)
	expectPublicationClaim(mock, events)
	mock.ExpectExec("UPDATE catalog_projection_releases SET publication_claim_id = NULL").
		WithArgs(testProjectionVersion, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	bus := &recordingCatalogEventBus{err: errors.New("subscriber unavailable")}
	reconciler := NewDBReconciler(db, bus)
	err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, catalogMergeResult(false))
	if err == nil || !commands.EventWasPersisted(err) {
		t.Fatalf("first reconciliation error = %v, want post-commit publication failure", err)
	}

	bus.err = nil
	expectExistingProjection(mock)
	expectPublication(mock, events)
	if err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, catalogMergeResult(false)); err != nil {
		t.Fatalf("publication retry: %v", err)
	}
	if bus.publishCalls != 2 || len(bus.published) != 1 {
		t.Fatalf("publication calls/events = (%d, %d), want retry with one successful event", bus.publishCalls, len(bus.published))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDBReconcilerUsesGlobalProviderExistenceAndActualModelDelta(t *testing.T) {
	db, mock := newCatalogSQLMock(t)
	types := eventTypesArgument{"catalog.provider.updated"}
	events := fixedJournalEvents(types...)
	expectNewProjection(mock, true, true, types)
	expectPublication(mock, events)

	result := catalogMergeResult(false)
	result.NewProviders = []string{"example"}
	result.Models = &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{
		"example": {Models: []validator.DefaultModel{{Name: "example-model", AddedInVersion: "1.0.0"}}},
	}}
	bus := &recordingCatalogEventBus{}
	reconciler := NewDBReconciler(db, bus)
	if err := reconciler.ReconcileFromCatalog(context.Background(), testProjectionVersion, testProjectionDigest, result); err != nil {
		t.Fatal(err)
	}
	assertEventTypes(t, bus.published, types...)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogAdditionMetadataDrivesFreshness(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		addedInVersion string
		isNew          bool
		want           string
	}{
		{name: "latest catalog addition", addedInVersion: "2.4.0", want: "new"},
		{name: "remote addition fallback", isNew: true, want: "new"},
		{name: "existing model", want: "stable"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := catalogModelFreshness(test.addedInVersion, test.isNew); got != test.want {
				t.Fatalf("freshness = %q, want %q", got, test.want)
			}
		})
	}
}

func newCatalogSQLMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return sqlx.NewDb(rawDB, "sqlmock"), mock
}

func expectNewProjection(mock sqlmock.Sqlmock, providerExists, includeModel bool, eventTypes eventTypesArgument) {
	expectProjectionBeforeJournal(mock, providerExists, includeModel, eventTypes)
	mock.ExpectExec("INSERT INTO catalog_projection_releases").
		WithArgs(testProjectionVersion, testProjectionDigest, eventTypes).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func expectProjectionBeforeJournal(mock sqlmock.Sqlmock, providerExists, includeModel bool, eventTypes eventTypesArgument) {
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE catalog_projection_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT bundle_sha256").WithArgs(testProjectionVersion).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT EXISTS\\(.*organization_id IS NULL").WithArgs("example").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(providerExists))
	mock.ExpectExec("INSERT INTO provider_configurations").WillReturnResult(sqlmock.NewResult(0, 1))
	if includeModel {
		mock.ExpectExec("INSERT INTO provider_model_status").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	for _, eventType := range eventTypes {
		mock.ExpectExec("INSERT INTO events").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), eventType, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func expectExistingProjection(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE catalog_projection_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT bundle_sha256").WithArgs(testProjectionVersion).
		WillReturnRows(sqlmock.NewRows([]string{"bundle_sha256"}).AddRow(testProjectionDigest))
	mock.ExpectCommit()
}

func expectPublication(mock sqlmock.Sqlmock, events []database.Event) {
	expectPublicationClaim(mock, events)
	mock.ExpectExec("UPDATE catalog_projection_releases SET events_published_at").
		WithArgs(testProjectionVersion, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectPublicationClaim(mock sqlmock.Sqlmock, events []database.Event) {
	encoded, _ := json.Marshal(events)
	mock.ExpectQuery("UPDATE catalog_projection_releases SET publication_claim_id").
		WithArgs(testProjectionVersion, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"events"}).AddRow(encoded))
}

func expectPublicationDone(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("UPDATE catalog_projection_releases SET publication_claim_id").
		WithArgs(testProjectionVersion, sqlmock.AnyArg()).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT events_published_at IS NOT NULL").
		WithArgs(testProjectionVersion).
		WillReturnRows(sqlmock.NewRows([]string{"completed"}).AddRow(true))
}

func fixedJournalEvents(types ...string) []database.Event {
	events := make([]database.Event, 0, len(types))
	for index, eventType := range types {
		events = append(events, database.Event{
			ID:        "catalog-event-" + string(rune('a'+index)),
			Type:      eventType,
			Stream:    "catalog-test",
			Payload:   []byte(`{"catalog":"test"}`),
			CreatedAt: 100 + int64(index),
		})
	}
	return events
}

func catalogMergeResult(newModel bool) *MergeResult {
	result := &MergeResult{Providers: map[string]interface{}{
		"providers": map[string]interface{}{
			"example": map[string]interface{}{"display_name": "Example"},
		},
	}}
	if newModel {
		result.Models = &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{
			"example": {Models: []validator.DefaultModel{{Name: "example-model"}}},
		}}
		result.NewModels = []string{"example/example-model"}
	}
	return result
}

func assertEventTypes(t *testing.T, events []database.Event, want ...string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(want), events)
	}
	for index := range want {
		if events[index].Type != want[index] {
			t.Fatalf("event %d type = %q, want %q", index, events[index].Type, want[index])
		}
	}
}
