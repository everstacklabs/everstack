package database

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const trooperProjectionTenant = "11111111-1111-1111-1111-111111111111"

func TestTrooperUpdatedProjectionScopesTheWriteToTheTenant(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE troopers SET updated_at = $1::timestamptz WHERE id = $2 AND tenant_id = $3")).
		WithArgs(sqlmock.AnyArg(), "trooper-1", trooperProjectionTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "trooper.updated", map[string]interface{}{
		"id":        "trooper-1",
		"tenant_id": trooperProjectionTenant,
	})
	if err := pm.handleTrooperUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTrooperDeletedProjectionScopesTheWriteToTheTenant(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE troopers SET deleted_at = $1::timestamptz, updated_at = $1::timestamptz WHERE id = $2 AND tenant_id = $3")).
		WithArgs(sqlmock.AnyArg(), "trooper-1", trooperProjectionTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "trooper.deleted", map[string]interface{}{
		"id":        "trooper-1",
		"tenant_id": trooperProjectionTenant,
	})
	if err := pm.handleTrooperDeleted(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTrooperWriteProjectionsRequireTenantIdentity(t *testing.T) {
	pm, _ := agentProjectionManager(t)
	for _, eventType := range []string{"trooper.updated", "trooper.deleted"} {
		t.Run(eventType, func(t *testing.T) {
			event := projectionUpdateEvent(t, eventType, map[string]interface{}{"id": "trooper-1"})
			var err error
			if eventType == "trooper.updated" {
				err = pm.handleTrooperUpdated(context.Background(), event)
			} else {
				err = pm.handleTrooperDeleted(context.Background(), event)
			}
			if err == nil {
				t.Fatal("projection error = nil, want missing tenant error")
			}
		})
	}
}
