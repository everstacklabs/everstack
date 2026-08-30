package annotations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestGetNextItemHandlerFiltersAndClaimsAssignableItems(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	xdb := sqlx.NewDb(db, "sqlmock")
	defer func() {
		_ = xdb.Close()
	}()

	columns := []string{
		"id", "queue_id", "tenant_id", "trace_id", "observation_id",
		"assigned_to", "assigned_at", "status", "priority",
		"completed_by", "completed_at", "created_at", "updated_at",
	}
	mock.ExpectQuery(`(?s)SELECT \* FROM annotation_queue_items.*AND \(assigned_to = \$3 OR assigned_to = ''\).*ORDER BY priority DESC, created_at ASC`).
		WithArgs("queue-1", "tenant-1", "user-1").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			"item-1", "queue-1", "tenant-1", "trace-1", "",
			"", nil, "pending", 10,
			"", nil, "2026-07-07T10:00:00Z", "2026-07-07T10:00:00Z",
		))
	mock.ExpectExec(`(?s)UPDATE annotation_queue_items.*AND \(assigned_to = \$1 OR assigned_to = ''\)`).
		WithArgs("user-1", sqlmock.AnyArg(), sqlmock.AnyArg(), "item-1", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	handler := NewGetNextItemHandler(xdb)
	got, err := handler.Handle(context.Background(), NewGetNextItemQuery("tenant-1", "queue-1", "user-1"))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	item, ok := got.(*QueueItemReadModel)
	if !ok {
		t.Fatalf("Handle() returned %T, want *QueueItemReadModel", got)
	}
	if item.ID != "item-1" {
		t.Fatalf("item ID = %q, want item-1", item.ID)
	}
	if !item.AssignedTo.Valid || item.AssignedTo.String != "user-1" {
		t.Fatalf("assigned_to = %#v, want user-1", item.AssignedTo)
	}
	if item.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", item.Status)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
