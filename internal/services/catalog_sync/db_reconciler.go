package catalog_sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/everstacklabs/everstack/internal/commands"
	catalogCommands "github.com/everstacklabs/everstack/internal/commands/handlers/catalog"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// CatalogDBReconciler defines the interface for syncing catalog to database
type CatalogDBReconciler interface {
	ReconcileFromCatalog(ctx context.Context, version, bundleSHA256 string, mergeResult *MergeResult) error
}

// DBReconciler atomically reconciles catalog projection tables.
type DBReconciler struct {
	db           *sqlx.DB
	providerRepo *provider_config.Repository
	modelRepo    *provider_config.ModelRepository
	eventBus     database.Bus
}

// NewDBReconciler creates a new database reconciler
func NewDBReconciler(db *sqlx.DB, eventBus database.Bus) *DBReconciler {
	return &DBReconciler{
		db:           db,
		providerRepo: provider_config.NewRepository(db),
		modelRepo:    provider_config.NewModelRepository(db),
		eventBus:     eventBus,
	}
}

// ReconcileFromCatalog syncs catalog data to database
func (r *DBReconciler) ReconcileFromCatalog(ctx context.Context, version, bundleSHA256 string, result *MergeResult) error {
	if version == "" || len(bundleSHA256) != 64 {
		return fmt.Errorf("catalog projection requires a version and SHA-256 bundle digest")
	}
	if r.db == nil {
		return fmt.Errorf("catalog database is not configured")
	}
	if err := r.ensureProjectionJournal(ctx, version, bundleSHA256, result); err != nil {
		return err
	}
	if err := r.publishJournalEvents(ctx, version); err != nil {
		return err
	}
	return nil
}

// ensureProjectionJournal commits the provider/model projection and its exact
// audit-event plan together. The table lock is held only for PostgreSQL work;
// no event-writer or bus I/O occurs while a database lock is open.
func (r *DBReconciler) ensureProjectionJournal(ctx context.Context, version, bundleSHA256 string, result *MergeResult) (err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin catalog reconciliation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE catalog_projection_releases IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock catalog projection journal: %w", err)
	}

	var existingDigest string
	err = tx.GetContext(ctx, &existingDigest, `
		SELECT bundle_sha256
		FROM catalog_projection_releases
		WHERE version = $1
	`, version)
	if err == nil {
		if existingDigest != bundleSHA256 {
			return fmt.Errorf("catalog version %q is already journaled with bundle %s, refusing conflicting bundle %s", version, existingDigest, bundleSHA256)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit catalog journal lookup: %w", err)
		}
		committed = true
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read catalog projection journal: %w", err)
	}

	providerEvents, err := r.syncProviders(ctx, tx, version, result)
	if err != nil {
		return fmt.Errorf("failed to sync providers: %w", err)
	}
	modelEvents, err := r.syncModels(ctx, tx, result)
	if err != nil {
		return fmt.Errorf("failed to sync models: %w", err)
	}
	events := append(providerEvents, modelEvents...)
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("encode catalog projection events: %w", err)
	}
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (stream, id, type, payload, created_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (stream, id, created_at) DO NOTHING
		`, event.Stream, event.ID, event.Type, event.Payload, event.CreatedAt); err != nil {
			return fmt.Errorf("persist catalog projection event %q: %w", event.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO catalog_projection_releases (version, bundle_sha256, events, events_persisted_at)
		VALUES ($1, $2, $3, NOW())
	`, version, bundleSHA256, encodedEvents); err != nil {
		return fmt.Errorf("write catalog projection journal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog reconciliation: %w", err)
	}
	committed = true
	return nil
}

func (r *DBReconciler) syncProviders(ctx context.Context, tx *sqlx.Tx, version string, result *MergeResult) ([]database.Event, error) {
	providerNames := r.extractProviderNames(result.Providers)
	sort.Strings(providerNames)
	events := make([]database.Event, 0, len(providerNames))
	for _, providerName := range providerNames {
		existed, err := r.providerRepo.ExistsTx(ctx, tx, providerName)
		if err != nil {
			return nil, fmt.Errorf("provider %q existence: %w", providerName, err)
		}
		if err := r.providerRepo.UpsertFromCatalogTx(ctx, tx, providerName, "available"); err != nil {
			return nil, fmt.Errorf("provider %q: %w", providerName, err)
		}
		providerData := r.getProviderData(result.Providers, providerName)
		if existed {
			events = append(events, catalogCommands.NewProviderUpdatedFromCatalogEvent(
				providerName, version, []string{"catalog_status"}, providerData,
			))
		} else {
			events = append(events, catalogCommands.NewProviderAddedFromCatalogEvent(
				providerName, "available", version, providerData,
			))
		}
	}
	return events, nil
}

func (r *DBReconciler) syncModels(ctx context.Context, tx *sqlx.Tx, result *MergeResult) ([]database.Event, error) {
	if result.Models == nil || len(result.Models.Providers) == 0 {
		return nil, nil
	}

	newModelSet := make(map[string]bool)
	for _, modelName := range result.NewModels {
		newModelSet[strings.ToLower(modelName)] = true
	}

	providerNames := make([]string, 0, len(result.Models.Providers))
	for providerName := range result.Models.Providers {
		providerNames = append(providerNames, providerName)
	}
	sort.Strings(providerNames)
	var events []database.Event
	for _, providerName := range providerNames {
		providerConfig := result.Models.Providers[providerName]
		for _, model := range providerConfig.Models {
			qualifiedName := providerName + "/" + model.Name
			detectedNew := newModelSet[strings.ToLower(qualifiedName)]
			freshness := catalogModelFreshness(model.AddedInVersion, detectedNew)
			if err := r.modelRepo.UpsertModelTx(ctx, tx, providerName, model.Name, detectedNew); err != nil {
				return nil, fmt.Errorf("model %q: %w", qualifiedName, err)
			}
			if detectedNew {
				events = append(events, catalogCommands.NewModelAddedFromCatalogEvent(
					providerName,
					model.Name,
					freshness,
					"available",
					map[string]interface{}{
						"name":               model.Name,
						"display_name":       model.DisplayName,
						"max_tokens":         model.MaxTokens,
						"capabilities":       model.Capabilities,
						"input_cost_per_1k":  model.InputCost,
						"output_cost_per_1k": model.OutputCost,
						"status":             model.Status,
						"release_date":       model.ReleaseDate,
					},
				))
			}
		}
	}
	return events, nil
}

func (r *DBReconciler) publishJournalEvents(ctx context.Context, version string) error {
	events, claimID, done, err := r.claimPublication(ctx, version)
	if err != nil || done {
		return err
	}
	if r.eventBus != nil && len(events) > 0 {
		// Delivery is at least once across process death. Catalog audit event IDs
		// are stable because the exact plan lives in the journal. No current
		// in-process subscriber consumes these event types; future subscribers
		// must deduplicate side effects by event ID.
		if err := r.eventBus.Publish(ctx, events...); err != nil {
			r.releasePublicationClaim(ctx, version, claimID)
			return &commands.PostCommitError{Err: fmt.Errorf("publish catalog events: %w", err)}
		}
	}
	if err := r.completePublication(ctx, version, claimID); err != nil {
		return &commands.PostCommitError{Err: fmt.Errorf("checkpoint published catalog events: %w", err)}
	}
	return nil
}

func (r *DBReconciler) claimPublication(ctx context.Context, version string) ([]database.Event, string, bool, error) {
	claimID := uuid.NewString()
	query := `
		UPDATE catalog_projection_releases
		SET publication_claim_id = $2, publication_claim_at = NOW()
		WHERE version = $1
		  AND events_persisted_at IS NOT NULL
		  AND events_published_at IS NULL
		  AND (publication_claim_at IS NULL OR publication_claim_at < NOW() - INTERVAL '15 minutes')
		RETURNING events
	`
	var encodedEvents []byte
	if err := r.db.GetContext(ctx, &encodedEvents, query, version, claimID); err != nil {
		if err != sql.ErrNoRows {
			return nil, "", false, fmt.Errorf("claim catalog event publication: %w", err)
		}
		var completed bool
		stateQuery := `
			SELECT events_published_at IS NOT NULL
			FROM catalog_projection_releases
			WHERE version = $1
		`
		if stateErr := r.db.GetContext(ctx, &completed, stateQuery, version); stateErr != nil {
			return nil, "", false, fmt.Errorf("read catalog event publication state: %w", stateErr)
		}
		if completed {
			return nil, "", true, nil
		}
		return nil, "", false, fmt.Errorf("catalog event publication for version %s is leased by another reconciler", version)
	}
	var events []database.Event
	if err := json.Unmarshal(encodedEvents, &events); err != nil {
		r.releasePublicationClaim(ctx, version, claimID)
		return nil, "", false, fmt.Errorf("decode journaled catalog events: %w", err)
	}
	return events, claimID, false, nil
}

func (r *DBReconciler) completePublication(ctx context.Context, version, claimID string) error {
	query := `
		UPDATE catalog_projection_releases
		SET events_published_at = NOW(),
			publication_claim_id = NULL,
			publication_claim_at = NULL,
			completed_at = NOW()
		WHERE version = $1 AND publication_claim_id = $2
	`
	result, err := r.db.ExecContext(ctx, query, version, claimID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("catalog event publication lease was lost")
	}
	return nil
}

func (r *DBReconciler) releasePublicationClaim(ctx context.Context, version, claimID string) {
	query := `
		UPDATE catalog_projection_releases
		SET publication_claim_id = NULL, publication_claim_at = NULL
		WHERE version = $1 AND publication_claim_id = $2
	`
	_, _ = r.db.ExecContext(ctx, query, version, claimID)
}

func catalogModelFreshness(addedInVersion string, detectedNew bool) string {
	if addedInVersion != "" || detectedNew {
		return "new"
	}
	return "stable"
}

// extractProviderNames extracts provider names from the merged providers map
func (r *DBReconciler) extractProviderNames(providers map[string]interface{}) []string {
	var names []string

	// Check if providers has the 'providers' key (raw YAML structure)
	if providersMap, ok := providers["providers"].(map[string]interface{}); ok {
		for name := range providersMap {
			names = append(names, name)
		}
	} else {
		// Direct provider map
		for name := range providers {
			names = append(names, name)
		}
	}

	return names
}

func (r *DBReconciler) getProviderData(providers map[string]interface{}, providerName string) map[string]interface{} {
	if providersMap, ok := providers["providers"].(map[string]interface{}); ok {
		if data, ok := providersMap[providerName].(map[string]interface{}); ok {
			return data
		}
	} else if data, ok := providers[providerName].(map[string]interface{}); ok {
		return data
	}
	return map[string]interface{}{}
}
