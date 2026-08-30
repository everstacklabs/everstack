package moderation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/hosting"
)

const maxActionTextBytes = 2_000

var ErrInvalidAction = errors.New("invalid moderation action")

type ActionKind string

const (
	ActionKindTakedown ActionKind = "takedown"
	ActionKindRestore  ActionKind = "restore"
)

type ActionStatus string

const (
	ActionStatusPending ActionStatus = "pending"
	// ActionStatusApplied means the serving provider accepted the projection;
	// it does not imply globally observed KV propagation.
	ActionStatusApplied ActionStatus = "applied"
	// ActionStatusSuperseded is a pending projection replaced by a newer
	// decision for the same slug. It must never be replayed at the edge.
	ActionStatusSuperseded ActionStatus = "superseded"
)

type Command struct {
	Slug           string
	Kind           ActionKind
	Reason         Reason
	Note           string
	ActorID        string
	IdempotencyKey string
}

type Action struct {
	ID             string       `db:"id"`
	SiteID         string       `db:"site_id"`
	Slug           string       `db:"slug"`
	Generation     int64        `db:"generation"`
	Kind           ActionKind   `db:"action"`
	Status         ActionStatus `db:"status"`
	Reason         Reason       `db:"reason"`
	Note           string       `db:"note"`
	ActorID        string       `db:"requested_by"`
	IdempotencyKey string       `db:"idempotency_key"`
	AttemptCount   int32        `db:"attempt_count"`
	LastError      string       `db:"last_error"`
	LeaseToken     string       `db:"lease_token"`
	CreatedAt      time.Time    `db:"created_at"`
	AppliedAt      *time.Time   `db:"applied_at"`
}

type AttemptOutcome struct {
	Applied bool
	Error   string
}

type ActionStore interface {
	BeginAction(ctx context.Context, command Command) (Action, error)
	GetAction(ctx context.Context, actionID string) (Action, error)
	CompleteAttempt(ctx context.Context, action Action, outcome AttemptOutcome) (Action, error)
	ListPending(ctx context.Context, limit int) ([]Action, error)
	// WithProjectionLock serializes writes for one slug across every gateway
	// replica. Implementations must hold the lock until fn returns.
	WithProjectionLock(ctx context.Context, slug string, fn func(context.Context) error) error
}

// EdgeEnforcer projects the desired moderation state to the serving edge.
// Implementations must make both takedown and restore idempotent.
type EdgeEnforcer interface {
	Apply(ctx context.Context, action Action) error
}

type EdgeEnforcerFunc func(context.Context, Action) error

func (f EdgeEnforcerFunc) Apply(ctx context.Context, action Action) error {
	return f(ctx, action)
}

// CoordinatedEdgeEnforcer writes the same generation to two independent
// serving-plane records: the R2 manifest is authoritative and KV is the
// low-latency accelerator. The Worker compares generations, so a delayed
// older write to either backend cannot win while the other has the newer
// decision.
type CoordinatedEdgeEnforcer struct {
	Authoritative EdgeEnforcer
	Accelerator   EdgeEnforcer
}

func (e CoordinatedEdgeEnforcer) Apply(ctx context.Context, action Action) error {
	var errs []error
	if e.Authoritative == nil {
		errs = append(errs, errors.New("authoritative moderation projection is not configured"))
	} else if err := e.Authoritative.Apply(ctx, action); err != nil {
		errs = append(errs, fmt.Errorf("authoritative projection: %w", err))
	}
	if e.Accelerator == nil {
		errs = append(errs, errors.New("moderation accelerator is not configured"))
	} else if err := e.Accelerator.Apply(ctx, action); err != nil {
		errs = append(errs, fmt.Errorf("accelerator projection: %w", err))
	}
	return errors.Join(errs...)
}

type UnavailableEdgeEnforcer struct {
	Reason string
}

func (e UnavailableEdgeEnforcer) Apply(context.Context, Action) error {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "edge enforcement is not configured"
	}
	return errors.New(reason)
}

type Controller struct {
	store ActionStore
	edge  EdgeEnforcer
}

func NewController(store ActionStore, edge EdgeEnforcer) *Controller {
	return &Controller{store: store, edge: edge}
}

// Execute records desired state before attempting the edge projection. Once
// the command is durable, an edge failure is returned as a pending action so
// callers never receive a false success and a reconciler can retry it.
func (c *Controller) Execute(ctx context.Context, command Command) (Action, error) {
	command.Slug = strings.ToLower(strings.TrimSpace(command.Slug))
	command.Note = strings.TrimSpace(command.Note)
	command.ActorID = strings.TrimSpace(command.ActorID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if err := validateCommand(command); err != nil {
		return Action{}, err
	}
	if c == nil || c.store == nil || c.edge == nil {
		return Action{}, errors.New("moderation enforcement is not configured")
	}
	action, err := c.store.BeginAction(ctx, command)
	if err != nil {
		return Action{}, err
	}
	if action.Status == ActionStatusApplied || action.Status == ActionStatusSuperseded {
		return action, nil
	}

	return c.project(ctx, action)
}

// ReconcilePending retries durable desired-state actions. Edge failures remain
// pending and are not returned as loop errors; storage failures stop the pass
// because they prevent the retry outcome from being recorded safely.
func (c *Controller) ReconcilePending(ctx context.Context, limit int) (int, error) {
	if c == nil || c.store == nil || c.edge == nil {
		return 0, errors.New("moderation enforcement is not configured")
	}
	actions, err := c.store.ListPending(ctx, limit)
	if err != nil {
		return 0, err
	}
	for i, action := range actions {
		if _, err := c.project(ctx, action); err != nil {
			return i, err
		}
	}
	return len(actions), nil
}

// project re-reads an action while holding the store's cross-replica slug
// lock. A newer decision may supersede an action between queue selection and
// projection; in that case it is returned without touching the edge.
func (c *Controller) project(ctx context.Context, selected Action) (Action, error) {
	result := selected
	err := c.store.WithProjectionLock(ctx, selected.Slug, func(lockCtx context.Context) error {
		current, err := c.store.GetAction(lockCtx, selected.ID)
		if err != nil {
			return err
		}
		result = current
		if current.Status != ActionStatusPending {
			return nil
		}

		applyErr := c.edge.Apply(lockCtx, current)
		outcome := AttemptOutcome{Applied: applyErr == nil}
		if applyErr != nil {
			outcome.Error = boundedError(applyErr)
		}
		result, err = c.store.CompleteAttempt(lockCtx, current, outcome)
		return err
	})
	if err != nil {
		return Action{}, err
	}
	return result, nil
}

func validateCommand(command Command) error {
	if !hosting.ValidSlug(command.Slug) {
		return fmt.Errorf("%w: invalid slug", ErrInvalidAction)
	}
	if command.Kind != ActionKindTakedown && command.Kind != ActionKindRestore {
		return fmt.Errorf("%w: invalid action kind", ErrInvalidAction)
	}
	if command.Kind == ActionKindTakedown {
		if _, ok := validReasons[command.Reason]; !ok {
			return fmt.Errorf("%w: takedown reason is required", ErrInvalidAction)
		}
	}
	if len(command.Note) > maxActionTextBytes {
		return fmt.Errorf("%w: note exceeds %d bytes", ErrInvalidAction, maxActionTextBytes)
	}
	if command.ActorID == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidAction)
	}
	if command.IdempotencyKey == "" || len(command.IdempotencyKey) > 128 {
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidAction)
	}
	return nil
}

func boundedError(err error) string {
	const max = 500
	message := err.Error()
	if len(message) <= max {
		return message
	}
	return message[:max]
}
