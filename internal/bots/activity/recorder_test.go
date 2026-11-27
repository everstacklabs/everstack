package activity

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/jmoiron/sqlx"
)

func TestRecordNeverWaitsForWorker(t *testing.T) {
	r := &Recorder{events: make(chan event, 1)}
	r.Record("key", "tenant", apikey.SurfaceMCP)
	for range 10000 {
		r.Record("key", "tenant", apikey.SurfaceMCP)
	}
	if r.dropped.Load() != 10000 || len(r.events) != 1 {
		t.Fatal("overflow was not counted")
	}
}
func TestFlushTransactionRetryAndLatestPerKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &Recorder{db: sqlx.NewDb(db, "sqlmock")}
	at := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	rows := map[rowKey]row{
		{"tenant", "key", "2026-11-10", apikey.SurfaceInference}: {calls: 2, latest: at},
		{"tenant", "key", "2026-11-10", apikey.SurfaceMCP}:       {calls: 3, latest: at.Add(time.Second)},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE api_keys").WithArgs(`{"key"}`, `{"tenant"}`, `{"2026-11-10T00:00:01Z"}`).WillReturnError(errors.New("offline"))
	mock.ExpectRollback()
	if err = r.flush(context.Background(), rows); err == nil {
		t.Fatal("expected failure")
	}
	if len(rows) != 2 {
		t.Fatal("failed batch lost counts")
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE api_keys").WithArgs(`{"key"}`, `{"tenant"}`, `{"2026-11-10T00:00:01Z"}`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO api_key_activity_daily").WithArgs(`{"tenant","tenant"}`, `{"key","key"}`, `{"2026-11-10","2026-11-10"}`, `{"inference","mcp"}`, `{2,3}`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err = r.flush(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestShutdownDrainsAndFlushes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE api_keys").WithArgs(`{"key"}`, `{"tenant"}`, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO api_key_activity_daily").WithArgs(`{"tenant"}`, `{"key"}`, sqlmock.AnyArg(), `{"telemetry"}`, `{100}`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	r := New(sqlx.NewDb(db, "sqlmock"))
	for range 100 {
		r.Record("key", "tenant", apikey.SurfaceTelemetry)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBacklogHasBoundedKeysAndRows(t *testing.T) {
	rows := map[rowKey]row{}
	keys := map[string]bool{}
	at := time.Now().UTC()
	for i := range maxKeys {
		if !aggregate(rows, keys, event{key: strconv.Itoa(i), tenant: "tenant", surface: apikey.SurfaceMCP, at: at}) {
			t.Fatal("early key cap")
		}
	}
	if aggregate(rows, keys, event{key: "overflow", tenant: "tenant", surface: apikey.SurfaceMCP, at: at}) {
		t.Fatal("key cap missing")
	}
	for i := 1; i < 8; i++ {
		for k := range keys {
			if !aggregate(rows, keys, event{key: k, tenant: "tenant", surface: apikey.SurfaceMCP, at: at.AddDate(0, 0, i)}) {
				t.Fatal("early row cap")
			}
		}
	}
	if aggregate(rows, keys, event{key: "0", tenant: "tenant", surface: apikey.SurfaceA2A, at: at}) {
		t.Fatal("retained row cap missing")
	}
	if !aggregate(rows, keys, event{key: "0", tenant: "tenant", surface: apikey.SurfaceMCP, at: at}) {
		t.Fatal("existing counters should remain writable")
	}
	if len(rows) != maxRows || len(keys) != maxKeys {
		t.Fatal("unbounded backlog")
	}
}

func TestStopRetriesFinalFlushAndReturnsFailure(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(strconv.FormatBool(recover), func(t *testing.T) {
			raw, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			unavailable := errors.New("final database failure")
			mock.ExpectBegin().WillReturnError(unavailable)
			if recover {
				mock.ExpectBegin()
				mock.ExpectExec("UPDATE api_keys").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("INSERT INTO api_key_activity_daily").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			r := New(sqlx.NewDb(raw, "sqlmock"))
			r.Record("key", "tenant", apikey.SurfaceMCP)
			budget := 40 * time.Millisecond
			if recover {
				budget = time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			err = r.Stop(ctx)
			if recover && err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			if !recover && !errors.Is(err, unavailable) {
				t.Fatalf("Stop lost final flush error: %v", err)
			}
			select {
			case <-r.done:
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFinalFlushRetryKeepsErrorWhenBudgetEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	failure := errors.New("flush still unavailable")
	attempts := 0
	err := retryFinalFlush(ctx, func(context.Context) error {
		attempts++
		if attempts == 2 {
			cancel()
		}
		return failure
	})
	if attempts != 2 || !errors.Is(err, failure) || !errors.Is(err, context.Canceled) {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
