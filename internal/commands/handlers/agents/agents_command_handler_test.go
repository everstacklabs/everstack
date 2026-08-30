package agents

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestHandleCreateAgentUsesCommandIDInCreatedEvent(t *testing.T) {
	handler := NewAgentsCommandHandler()
	cmd := NewCreateAgentCommand(
		"tenant-1",
		"Research Agent",
		"runs research tasks",
		"gpt-4o-mini",
		"be helpful",
		[]string{"web_search"},
		nil,
		25,
		10,
		"primary",
		nil,
		"ask",
		false,
		nil,
		nil,
		nil,
		"",
		nil,
		"",
		"",
		"",
		"",
		"",
		"",
		0,
		0,
		0,
		0,
		nil,
		nil,
		false,
		"",
		"",
		"",
		"",
		"",
		0,
		nil,
		false,
		"user-1",
		"",
	)

	events, err := handler.handleCreateAgent(context.Background(), cmd)
	if err != nil {
		t.Fatalf("handleCreateAgent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("handleCreateAgent() emitted %d events, want 1", len(events))
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if got := payload["id"]; got != cmd.ID {
		t.Fatalf("payload id = %v, want command id %s", got, cmd.ID)
	}
	if got := payload["lifecycle_mode"]; got != "ephemeral" {
		t.Fatalf("payload lifecycle_mode = %v, want ephemeral", got)
	}
}

func TestHandleUpdateAgentPreservesExplicitEmptyTools(t *testing.T) {
	handler := NewAgentsCommandHandler()
	cmd := NewUpdateAgentCommand("agent-1", "tenant-1", "user-1", "")
	cmd.Tools = []string{}

	events, err := handler.handleUpdateAgent(context.Background(), cmd)
	if err != nil {
		t.Fatalf("handleUpdateAgent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("handleUpdateAgent() emitted %d events, want 1", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("payload tools = %#v, want explicit empty list", payload["tools"])
	}
}

func TestHandleCreateSessionCapturesActiveRevisionBeforeProjection(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	mock.ExpectQuery(`SELECT active_revision_id`).
		WithArgs("agent-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"active_revision_id"}).AddRow("revision-7"))

	handler := NewAgentsCommandHandler(sqlx.NewDb(rawDB, "sqlmock"))
	cmd := NewCreateSessionCommand("tenant-1", "agent-1", nil, "user-1", "")
	events, err := handler.handleCreateSession(context.Background(), cmd)
	if err != nil {
		t.Fatalf("handleCreateSession() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("handleCreateSession() emitted %d events, want 1", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["revision_id"] != "revision-7" {
		t.Fatalf("revision_id = %#v, want revision-7", payload["revision_id"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
