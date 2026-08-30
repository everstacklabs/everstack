package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func newAuthzTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	xdb := sqlx.NewDb(db, "sqlmock")
	s := CreateServer()
	s.SetDB(xdb)

	return s, mock, func() {
		_ = xdb.Close()
	}
}

func TestQueueAnnotatorAuthorized(t *testing.T) {
	tests := []struct {
		name       string
		annotators []string
		userID     string
		want       bool
	}{
		{name: "restricted member", annotators: []string{"user-1", "user-2"}, userID: "user-1", want: true},
		{name: "restricted non-member", annotators: []string{"user-1"}, userID: "user-2", want: false},
		{name: "restricted empty user", annotators: []string{"user-1"}, userID: "", want: false},
		{name: "open queue", annotators: nil, userID: "user-2", want: true},
		{name: "open queue empty user", annotators: nil, userID: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := queueAnnotatorAuthorized(tt.annotators, tt.userID); got != tt.want {
				t.Fatalf("queueAnnotatorAuthorized() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAssertQueueAnnotator(t *testing.T) {
	tests := []struct {
		name              string
		annotatorsLiteral string
		userID            string
		wantCode          connect.Code
	}{
		{
			name:              "restricted member allowed",
			annotatorsLiteral: "{user-1,user-2}",
			userID:            "user-1",
		},
		{
			name:              "restricted non-member denied",
			annotatorsLiteral: "{user-1,user-2}",
			userID:            "user-3",
			wantCode:          connect.CodePermissionDenied,
		},
		{
			name:              "restricted empty user denied",
			annotatorsLiteral: "{user-1,user-2}",
			userID:            "",
			wantCode:          connect.CodePermissionDenied,
		},
		{
			name:              "open queue allowed",
			annotatorsLiteral: "{}",
			userID:            "user-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, mock, cleanup := newAuthzTestServer(t)
			defer cleanup()

			mock.ExpectQuery(`SELECT annotators\s+FROM annotation_queues\s+WHERE id = \$1 AND tenant_id = \$2`).
				WithArgs("queue-1", "tenant-1").
				WillReturnRows(sqlmock.NewRows([]string{"annotators"}).AddRow(tt.annotatorsLiteral))

			err := s.assertQueueAnnotator(context.Background(), "queue-1", "tenant-1", tt.userID)
			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("assertQueueAnnotator() error = %v, want nil", err)
				}
			} else {
				var connectErr *connect.Error
				if !errors.As(err, &connectErr) || connectErr.Code() != tt.wantCode {
					t.Fatalf("assertQueueAnnotator() error = %v, want connect code %v", err, tt.wantCode)
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet sql expectations: %v", err)
			}
		})
	}
}

func TestAssertItemQueueAnnotatorDeniesRestrictedNonMember(t *testing.T) {
	s, mock, cleanup := newAuthzTestServer(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT queue_id\s+FROM annotation_queue_items\s+WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs("item-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"queue_id"}).AddRow("queue-1"))
	mock.ExpectQuery(`SELECT annotators\s+FROM annotation_queues\s+WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs("queue-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"annotators"}).AddRow("{user-1}"))

	err := s.assertItemQueueAnnotator(context.Background(), "item-1", "tenant-1", "user-2")
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("assertItemQueueAnnotator() error = %v, want PermissionDenied", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
