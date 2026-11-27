package serve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// Every organization in a tick is read inside one gateway-DB transaction (it
// holds the reporter's advisory lock). In Postgres a failed statement aborts
// the whole transaction, so without a savepoint per organization one bad read
// fails every organization after it with "current transaction is aborted".
// The mock stands in for Postgres here: the second organization's read is
// only accepted after the first one's failure was rolled back to a savepoint.
func TestInferenceUsageReporterIsolatesOrganizationsWithSavepoints(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const orgA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const orgB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	sumQuery := `SELECT COALESCE\(SUM\(requests\), 0\)::BIGINT AS requests`

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"led"}).AddRow(true))
	mock.ExpectQuery(`SELECT DISTINCT organization_id`).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(orgA).AddRow(orgB))
	mock.ExpectExec(`SAVEPOINT inference_usage_org`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sumQuery).WithArgs(orgA, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(errors.New("canceling statement due to statement timeout"))
	mock.ExpectExec(`ROLLBACK TO SAVEPOINT inference_usage_org`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SAVEPOINT inference_usage_org`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sumQuery).WithArgs(orgB, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"requests", "input_tokens", "output_tokens"}).AddRow(int64(2), int64(10), int64(4)))
	mock.ExpectExec(`RELEASE SAVEPOINT inference_usage_org`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		mu.Lock()
		posted = append(posted, string(body[:n]))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newInferenceUsageReporter(sqlx.NewDb(db, "sqlmock"), nil, srv.URL, srv.Client())
	r.now = func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) }
	led, err := r.reportOnce(context.Background())
	if !led {
		t.Fatal("reporter did not lead the tick")
	}
	if err == nil || !strings.Contains(err.Error(), orgA) {
		t.Fatalf("err = %v, want organization A's failure reported", err)
	}
	if len(posted) != 1 || !strings.Contains(posted[0], orgB) {
		t.Fatalf("posted = %v, want organization B reported after A failed", posted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
