package annotations

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func newCommandHandlerTestDB(t *testing.T) (*AnnotationsCommandHandler, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	xdb := sqlx.NewDb(db, "sqlmock")

	return NewAnnotationsCommandHandler(xdb), mock, func() {
		_ = xdb.Close()
	}
}

func TestHandleSubmitAnnotationEnforcesAssignmentGuard(t *testing.T) {
	tests := []struct {
		name        string
		rows        int64
		wantErr     bool
		description string
	}{
		{
			name:        "assigned to another user denied",
			rows:        0,
			wantErr:     true,
			description: "zero rows is how Postgres reports the atomic assigned_to guard rejecting the caller",
		},
		{
			name:        "own or unassigned item allowed",
			rows:        1,
			description: "one row covers assigned_to=user or assigned_to='' because both satisfy the guarded WHERE clause",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, mock, cleanup := newCommandHandlerTestDB(t)
			defer cleanup()

			mock.ExpectExec(`(?s)UPDATE annotation_queue_items.*tenant_id = \$6.*\(assigned_to = \$5 OR assigned_to = ''\).*ANY\(annotation_queues\.annotators\)`).
				WithArgs("item-1", "user-1", sqlmock.AnyArg(), sqlmock.AnyArg(), "user-1", "tenant-1").
				WillReturnResult(sqlmock.NewResult(0, tt.rows))

			cmd := NewSubmitAnnotationCommand("tenant-1", "item-1", "user-1", nil, "user-1", "")
			events, err := handler.handleSubmitAnnotation(context.Background(), cmd)
			if tt.wantErr {
				if !errors.Is(err, ErrAnnotationItemPermissionDenied) {
					t.Fatalf("%s: error = %v, want ErrAnnotationItemPermissionDenied", tt.description, err)
				}
				if len(events) != 0 {
					t.Fatalf("events = %d, want 0", len(events))
				}
			} else {
				if err != nil {
					t.Fatalf("%s: error = %v, want nil", tt.description, err)
				}
				if len(events) != 1 {
					t.Fatalf("events = %d, want 1", len(events))
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet sql expectations: %v", err)
			}
		})
	}
}

func TestHandleSkipItemEnforcesAssignmentGuard(t *testing.T) {
	tests := []struct {
		name    string
		rows    int64
		wantErr bool
	}{
		{name: "assigned to another user denied", rows: 0, wantErr: true},
		{name: "own or unassigned item allowed", rows: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, mock, cleanup := newCommandHandlerTestDB(t)
			defer cleanup()

			mock.ExpectExec(`(?s)UPDATE annotation_queue_items.*tenant_id = \$5.*\(assigned_to = \$4 OR assigned_to = ''\).*ANY\(annotation_queues\.annotators\)`).
				WithArgs("item-1", sqlmock.AnyArg(), sqlmock.AnyArg(), "user-1", "tenant-1").
				WillReturnResult(sqlmock.NewResult(0, tt.rows))

			cmd := NewSkipItemCommand("tenant-1", "item-1", "user-1", "user-1", "")
			events, err := handler.handleSkipItem(context.Background(), cmd)
			if tt.wantErr {
				if !errors.Is(err, ErrAnnotationItemPermissionDenied) {
					t.Fatalf("error = %v, want ErrAnnotationItemPermissionDenied", err)
				}
				if len(events) != 0 {
					t.Fatalf("events = %d, want 0", len(events))
				}
			} else {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				if len(events) != 1 {
					t.Fatalf("events = %d, want 1", len(events))
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet sql expectations: %v", err)
			}
		})
	}
}
