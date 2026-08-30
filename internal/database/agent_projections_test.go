package database

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func projectionUpdateEvent(t *testing.T, eventType string, payload map[string]interface{}) Event {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return Event{Type: eventType, Payload: data}
}

func agentProjectionManager(t *testing.T) (*ProjectionManager, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return NewProjectionManager(sqlx.NewDb(rawDB, "sqlmock"), nil), mock
}

func TestAgentUpdatedProjectsAgentConfigurationFields(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	query := regexp.QuoteMeta("UPDATE agent_definitions SET updated_at = $2::timestamptz, mode = $3, max_steps = $4, task_permission_mode = $5, hidden = $6, color = $7, working_directory = $8, mention_alias = $9 WHERE id = $1")
	mock.ExpectExec(query).
		WithArgs(
			"agent-1", "2026-08-09T12:00:00Z", "primary", float64(12), "deny",
			true, "violet", "/workspace", "reviewer",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "agent.updated", map[string]interface{}{
		"id":                   "agent-1",
		"updated_at":           "2026-08-09T12:00:00Z",
		"mode":                 "primary",
		"max_steps":            12,
		"task_permission_mode": "deny",
		"hidden":               true,
		"color":                "violet",
		"working_directory":    "/workspace",
		"mention_alias":        "reviewer",
	})
	if err := pm.handleAgentUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionUpdatedProjectsModeOnce(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	query := regexp.QuoteMeta("UPDATE functions SET updated_at = $2::timestamptz, mode = $3 WHERE id = $1")
	mock.ExpectExec(query).
		WithArgs("function-1", "2026-08-09T12:00:00Z", "isolated").
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "function.updated", map[string]interface{}{
		"id":         "function-1",
		"updated_at": "2026-08-09T12:00:00Z",
		"mode":       "isolated",
	})
	if err := pm.handleFunctionUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionUpdatedIgnoresAgentOnlyFields(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	query := regexp.QuoteMeta("UPDATE functions SET updated_at = $2::timestamptz WHERE id = $1")
	mock.ExpectExec(query).
		WithArgs("function-1", "2026-08-09T12:00:00Z").
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "function.updated", map[string]interface{}{
		"id":                   "function-1",
		"updated_at":           "2026-08-09T12:00:00Z",
		"max_steps":            12,
		"task_permission_mode": "deny",
		"hidden":               true,
		"color":                "violet",
		"working_directory":    "/workspace",
		"mention_alias":        "reviewer",
	})
	if err := pm.handleFunctionUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
