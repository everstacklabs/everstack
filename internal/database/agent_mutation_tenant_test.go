package database

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

const agentProjectionTenant = "11111111-1111-1111-1111-111111111111"

func TestAgentUpdatedProjectionScopesTheWriteToTheTenant(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_definitions SET updated_at = $2::timestamptz WHERE id = $1 AND tenant_id = $3")).
		WithArgs("agent-1", "2026-11-16T12:00:00Z", agentProjectionTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "agent.updated", map[string]interface{}{
		"id":         "agent-1",
		"tenant_id":  agentProjectionTenant,
		"updated_at": "2026-11-16T12:00:00Z",
	})
	if err := pm.handleAgentUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentDeletedProjectionScopesAllWritesToTheTenant(t *testing.T) {
	pm, mock := agentProjectionManager(t)
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE agent_definitions
		SET deleted_at = $2::timestamptz, enabled = FALSE, updated_at = $2::timestamptz
		WHERE id = $1 AND tenant_id = $3 AND deleted_at IS NULL
	`)).
		WithArgs("agent-1", "2026-11-16T12:00:00Z", agentProjectionTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE sandbox_crons SET enabled = FALSE, updated_at = NOW()
		WHERE tenant_id = $2
		AND session_id IN (SELECT id::text FROM agent_sessions WHERE agent_id = $1 AND tenant_id::text = $2)
	`)).
		WithArgs("agent-1", agentProjectionTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE sandbox_webhooks SET enabled = FALSE, updated_at = NOW()
		WHERE tenant_id = $2
		AND session_id IN (SELECT id::text FROM agent_sessions WHERE agent_id = $1 AND tenant_id::text = $2)
	`)).
		WithArgs("agent-1", agentProjectionTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := projectionUpdateEvent(t, "agent.deleted", map[string]interface{}{
		"id":         "agent-1",
		"tenant_id":  agentProjectionTenant,
		"deleted_at": "2026-11-16T12:00:00Z",
	})
	if err := pm.handleAgentDeleted(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentDeletedProjectionReturnsCleanupErrors(t *testing.T) {
	deletedEvent := projectionUpdateEvent(t, "agent.deleted", map[string]interface{}{
		"id":         "agent-1",
		"tenant_id":  agentProjectionTenant,
		"deleted_at": "2026-11-16T12:00:00Z",
	})
	deleteQuery := regexp.QuoteMeta(`
		UPDATE agent_definitions
		SET deleted_at = $2::timestamptz, enabled = FALSE, updated_at = $2::timestamptz
		WHERE id = $1 AND tenant_id = $3 AND deleted_at IS NULL
	`)
	cronQuery := regexp.QuoteMeta(`
		UPDATE sandbox_crons SET enabled = FALSE, updated_at = NOW()
		WHERE tenant_id = $2
		AND session_id IN (SELECT id::text FROM agent_sessions WHERE agent_id = $1 AND tenant_id::text = $2)
	`)
	webhookQuery := regexp.QuoteMeta(`
		UPDATE sandbox_webhooks SET enabled = FALSE, updated_at = NOW()
		WHERE tenant_id = $2
		AND session_id IN (SELECT id::text FROM agent_sessions WHERE agent_id = $1 AND tenant_id::text = $2)
	`)

	t.Run("cron cleanup", func(t *testing.T) {
		pm, mock := agentProjectionManager(t)
		cleanupErr := errors.New("cron cleanup failed")
		mock.ExpectExec(deleteQuery).
			WithArgs("agent-1", "2026-11-16T12:00:00Z", agentProjectionTenant).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(cronQuery).
			WithArgs("agent-1", agentProjectionTenant).
			WillReturnError(cleanupErr)

		if err := pm.handleAgentDeleted(context.Background(), deletedEvent); !errors.Is(err, cleanupErr) {
			t.Fatalf("projection error = %v, want cron cleanup error", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("webhook cleanup", func(t *testing.T) {
		pm, mock := agentProjectionManager(t)
		cleanupErr := errors.New("webhook cleanup failed")
		mock.ExpectExec(deleteQuery).
			WithArgs("agent-1", "2026-11-16T12:00:00Z", agentProjectionTenant).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(cronQuery).
			WithArgs("agent-1", agentProjectionTenant).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(webhookQuery).
			WithArgs("agent-1", agentProjectionTenant).
			WillReturnError(cleanupErr)

		if err := pm.handleAgentDeleted(context.Background(), deletedEvent); !errors.Is(err, cleanupErr) {
			t.Fatalf("projection error = %v, want webhook cleanup error", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAgentDeletedProjectionExecutesAgainstPostgres(t *testing.T) {
	db := pgscratch.New(t, "agent_deleted_projection")
	pgscratch.MustExec(t, db, `
		CREATE TABLE agent_definitions (
			id UUID PRIMARY KEY,
			tenant_id UUID NOT NULL,
			deleted_at TIMESTAMPTZ,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, `
		CREATE TABLE agent_sessions (
			id UUID PRIMARY KEY,
			tenant_id UUID NOT NULL,
			agent_id UUID NOT NULL REFERENCES agent_definitions(id)
		)`, `
		CREATE TABLE sandbox_crons (
			tenant_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, `
		CREATE TABLE sandbox_webhooks (
			tenant_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, `
		INSERT INTO agent_definitions (id, tenant_id)
		VALUES ('22222222-2222-2222-2222-222222222222', '`+agentProjectionTenant+`')`, `
		INSERT INTO agent_sessions (id, tenant_id, agent_id)
		VALUES ('33333333-3333-3333-3333-333333333333', '`+agentProjectionTenant+`', '22222222-2222-2222-2222-222222222222')`, `
		INSERT INTO sandbox_crons (tenant_id, session_id)
		VALUES ('`+agentProjectionTenant+`', '33333333-3333-3333-3333-333333333333')`, `
		INSERT INTO sandbox_webhooks (tenant_id, session_id)
		VALUES ('`+agentProjectionTenant+`', '33333333-3333-3333-3333-333333333333')`)

	pm := NewProjectionManager(db, nil)
	event := projectionUpdateEvent(t, "agent.deleted", map[string]interface{}{
		"id":         "22222222-2222-2222-2222-222222222222",
		"tenant_id":  agentProjectionTenant,
		"deleted_at": "2026-11-16T12:00:00Z",
	})
	if err := pm.handleAgentDeleted(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}

	for _, table := range []string{"sandbox_crons", "sandbox_webhooks"} {
		var enabled bool
		if err := db.Get(&enabled, "SELECT enabled FROM "+table+" WHERE tenant_id = $1", agentProjectionTenant); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if enabled {
			t.Fatalf("%s remains enabled", table)
		}
	}
}

func TestAgentWriteProjectionsRequireTenantIdentity(t *testing.T) {
	pm, _ := agentProjectionManager(t)
	for _, eventType := range []string{"agent.updated", "agent.deleted"} {
		t.Run(eventType, func(t *testing.T) {
			event := projectionUpdateEvent(t, eventType, map[string]interface{}{
				"id":         "agent-1",
				"updated_at": "2026-11-16T12:00:00Z",
				"deleted_at": "2026-11-16T12:00:00Z",
			})
			var err error
			if eventType == "agent.updated" {
				err = pm.handleAgentUpdated(context.Background(), event)
			} else {
				err = pm.handleAgentDeleted(context.Background(), event)
			}
			if err == nil {
				t.Fatal("projection error = nil, want missing tenant error")
			}
		})
	}
}
