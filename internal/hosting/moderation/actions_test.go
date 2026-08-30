package moderation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
)

type recordingActionStore struct {
	next        moderation.Action
	current     *moderation.Action
	pending     []moderation.Action
	commands    []moderation.Command
	outcomes    []moderation.AttemptOutcome
	beginErr    error
	completeErr error
}

func (s *recordingActionStore) BeginAction(_ context.Context, command moderation.Command) (moderation.Action, error) {
	s.commands = append(s.commands, command)
	if s.beginErr != nil {
		return moderation.Action{}, s.beginErr
	}
	return s.next, nil
}

func (s *recordingActionStore) GetAction(_ context.Context, _ string) (moderation.Action, error) {
	if s.current != nil {
		return *s.current, nil
	}
	return s.next, nil
}

func (s *recordingActionStore) CompleteAttempt(_ context.Context, action moderation.Action, outcome moderation.AttemptOutcome) (moderation.Action, error) {
	s.outcomes = append(s.outcomes, outcome)
	if s.completeErr != nil {
		return moderation.Action{}, s.completeErr
	}
	if outcome.Applied {
		action.Status = moderation.ActionStatusApplied
	} else {
		action.Status = moderation.ActionStatusPending
		action.LastError = outcome.Error
	}
	return action, nil
}

func (s *recordingActionStore) ListPending(_ context.Context, _ int) ([]moderation.Action, error) {
	return append([]moderation.Action(nil), s.pending...), nil
}

func (s *recordingActionStore) WithProjectionLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

type recordingEdge struct {
	actions []moderation.Action
	err     error
}

func (e *recordingEdge) Apply(_ context.Context, action moderation.Action) error {
	e.actions = append(e.actions, action)
	return e.err
}

func TestControllerAppliesAndRecordsATakedown(t *testing.T) {
	store := &recordingActionStore{next: moderation.Action{
		ID: "action-1", Slug: "release-notes", Kind: moderation.ActionKindTakedown, Status: moderation.ActionStatusPending,
	}}
	edge := &recordingEdge{}
	controller := moderation.NewController(store, edge)

	action, err := controller.Execute(context.Background(), moderation.Command{
		Slug:           "release-notes",
		Kind:           moderation.ActionKindTakedown,
		Reason:         moderation.ReasonPhishing,
		Note:           "Credential collection page confirmed.",
		ActorID:        "operator-1",
		IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatalf("execute takedown: %v", err)
	}
	if action.Status != moderation.ActionStatusApplied {
		t.Fatalf("status = %q, want applied", action.Status)
	}
	if len(edge.actions) != 1 || edge.actions[0].Slug != "release-notes" {
		t.Fatalf("edge actions = %+v", edge.actions)
	}
	if len(store.outcomes) != 1 || !store.outcomes[0].Applied {
		t.Fatalf("stored outcomes = %+v", store.outcomes)
	}
}

func TestControllerLeavesEnforcementPendingWhenTheEdgeFails(t *testing.T) {
	store := &recordingActionStore{next: moderation.Action{
		ID: "action-2", Slug: "release-notes", Kind: moderation.ActionKindTakedown, Status: moderation.ActionStatusPending,
	}}
	edge := &recordingEdge{err: errors.New("cloudflare unavailable")}
	controller := moderation.NewController(store, edge)

	action, err := controller.Execute(context.Background(), moderation.Command{
		Slug:           "release-notes",
		Kind:           moderation.ActionKindTakedown,
		Reason:         moderation.ReasonMalware,
		ActorID:        "operator-1",
		IdempotencyKey: "request-2",
	})
	if err != nil {
		t.Fatalf("accepted takedown returned a transport error: %v", err)
	}
	if action.Status != moderation.ActionStatusPending || action.LastError == "" {
		t.Fatalf("action = %+v, want pending with an error", action)
	}
	if len(store.outcomes) != 1 || store.outcomes[0].Applied {
		t.Fatalf("stored outcomes = %+v", store.outcomes)
	}
}

func TestControllerReconcilesPendingEdgeActions(t *testing.T) {
	pending := moderation.Action{
		ID: "action-3", Slug: "release-notes", Kind: moderation.ActionKindRestore, Status: moderation.ActionStatusPending,
	}
	store := &recordingActionStore{next: pending, pending: []moderation.Action{pending}}
	edge := &recordingEdge{}
	controller := moderation.NewController(store, edge)

	attempted, err := controller.ReconcilePending(context.Background(), 50)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if attempted != 1 {
		t.Fatalf("attempted = %d, want 1", attempted)
	}
	if len(edge.actions) != 1 || edge.actions[0].Kind != moderation.ActionKindRestore {
		t.Fatalf("edge actions = %+v", edge.actions)
	}
	if len(store.outcomes) != 1 || !store.outcomes[0].Applied {
		t.Fatalf("stored outcomes = %+v", store.outcomes)
	}
}

func TestControllerDoesNotReplayASupersededAction(t *testing.T) {
	store := &recordingActionStore{next: moderation.Action{
		ID: "action-old", Slug: "release-notes", Kind: moderation.ActionKindTakedown,
		Status: moderation.ActionStatusSuperseded,
	}}
	edge := &recordingEdge{}
	controller := moderation.NewController(store, edge)

	action, err := controller.Execute(context.Background(), moderation.Command{
		Slug: "release-notes", Kind: moderation.ActionKindTakedown, Reason: moderation.ReasonPhishing,
		ActorID: "operator-1", IdempotencyKey: "request-old",
	})
	if err != nil {
		t.Fatalf("execute superseded action: %v", err)
	}
	if action.Status != moderation.ActionStatusSuperseded {
		t.Fatalf("status = %q, want superseded", action.Status)
	}
	if len(edge.actions) != 0 || len(store.outcomes) != 0 {
		t.Fatalf("superseded action was replayed: edge=%+v outcomes=%+v", edge.actions, store.outcomes)
	}
}

func TestControllerRechecksSupersessionInsideTheProjectionLock(t *testing.T) {
	selected := moderation.Action{
		ID: "action-old", SiteID: "site-1", Slug: "release-notes", Generation: 1,
		Kind: moderation.ActionKindTakedown, Status: moderation.ActionStatusPending,
	}
	superseded := selected
	superseded.Status = moderation.ActionStatusSuperseded
	store := &recordingActionStore{next: selected, current: &superseded}
	edge := &recordingEdge{}
	controller := moderation.NewController(store, edge)

	action, err := controller.Execute(context.Background(), moderation.Command{
		Slug: "release-notes", Kind: moderation.ActionKindTakedown, Reason: moderation.ReasonPhishing,
		ActorID: "operator-1", IdempotencyKey: "request-raced",
	})
	if err != nil {
		t.Fatalf("execute raced action: %v", err)
	}
	if action.Status != moderation.ActionStatusSuperseded {
		t.Fatalf("status = %q, want superseded", action.Status)
	}
	if len(edge.actions) != 0 || len(store.outcomes) != 0 {
		t.Fatalf("superseded action reached projection: edge=%+v outcomes=%+v", edge.actions, store.outcomes)
	}
}
