package eval_runner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/query"
	traceshandler "github.com/everstacklabs/everstack/internal/query/handlers/traces"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// RunnerOpts configures the eval runner with optional dependencies.
type RunnerOpts struct {
	DB          *sqlx.DB
	CHConn      clickhouse.Conn
	VectorStore memory.VectorStore       // optional
	Embedder    memory.EmbedderInterface // optional
	SandboxMgr  *sandbox.SandboxManager  // optional

	// AllowUnsandboxedScorers is a server-gated escape hatch: when true, code
	// scorers with use_sandbox=false run in-process on this host. Default
	// false — code scorers execute arbitrary user code, so without this flag
	// they either run in a sandbox or fail closed.
	AllowUnsandboxedScorers bool
}

// RegressionNotifier is called when a regression is detected after a run completes.
type RegressionNotifier interface {
	NotifyRegression(ctx context.Context, tenantID, runID string, result *RegressionResult)
}

type Runner struct {
	db           *sqlx.DB
	chConn       clickhouse.Conn
	pollInterval time.Duration
	leaseTTL     time.Duration
	claimBatch   int
	workerID     string

	retriever     *EvalRetriever     // nil if no vector store
	sandboxScorer *SandboxScorer     // nil if no sandbox manager
	regNotifier   RegressionNotifier // nil until set via SetRegressionNotifier
	scoreRecorder *scores.Recorder   // nil until set via SetScoreRecorder (used by sampling runner)
	judgeCall     judgeCallFunc

	// allowUnsandboxedScorers gates in-process execution of code scorers with
	// use_sandbox=false. Zero value (false) means such scorers are refused.
	allowUnsandboxedScorers bool
}

const (
	defaultEvalRunLeaseTTL   = 60 * time.Second
	defaultEvalRunClaimBatch = 10
)

var errEvalRunLeaseLost = errors.New("eval run lease lost")

// SetRegressionNotifier sets the notifier that fires alerts on regressions.
// Called after alert system initialization (which happens after runner start).
func (r *Runner) SetRegressionNotifier(n RegressionNotifier) {
	r.regNotifier = n
}

type evalRunRow struct {
	ID               string         `db:"id"`
	TenantID         string         `db:"tenant_id"`
	DatasetID        string         `db:"dataset_id"`
	DatasetVersionID string         `db:"dataset_version_id"`
	Status           string         `db:"status"`
	EvalTargetType   string         `db:"eval_target_type"`
	EvalTargetID     string         `db:"eval_target_id"`
	EvalConfig       []byte         `db:"eval_config"`
	ScorerConfigIDs  pq.StringArray `db:"scorer_config_ids"`
}

type pendingItemRow struct {
	ID                  string `db:"id"`
	DatasetItemID       string `db:"dataset_item_id"`
	Input               []byte `db:"input"`
	ExpectedOutput      []byte `db:"expected_output"`
	SourceTraceID       string `db:"source_trace_id"`
	SourceObservationID string `db:"source_observation_id"`
	Metadata            []byte `db:"metadata"`
}

// Start initializes the eval runner and begins polling for pending runs.
func Start(ctx context.Context, opts RunnerOpts) *Runner {
	r := &Runner{
		db:           opts.DB,
		chConn:       opts.CHConn,
		pollInterval: 2 * time.Second,
		leaseTTL:     defaultEvalRunLeaseTTL,
		claimBatch:   defaultEvalRunClaimBatch,
		workerID:     uuid.New().String(),

		allowUnsandboxedScorers: opts.AllowUnsandboxedScorers,
	}

	// Wire retriever if vector store is available
	if opts.VectorStore != nil || opts.CHConn != nil {
		r.retriever = NewEvalRetriever(opts.CHConn, opts.VectorStore, opts.Embedder)
	}

	// Wire sandbox scorer if sandbox manager is available
	if opts.SandboxMgr != nil {
		r.sandboxScorer = NewSandboxScorer(opts.SandboxMgr)
	}

	go r.loop(ctx)
	return r
}

func (r *Runner) loop(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	claims, err := r.claimRuns(ctx)
	if err != nil {
		logger.WithError(err).Warn("eval_runner: failed to claim runs")
		return
	}

	for _, claim := range claims {
		if err := r.resetStaleRunningItems(ctx, claim.ID); err != nil {
			logger.WithFields("eval_run_id", claim.ID).WithError(err).Warn("eval_runner: failed to reset stale running items")
			r.releaseLeaseOnNonTerminalExit(claim.ID, claim.LeaseEpoch)
			continue
		}

		run, err := r.getClaimedRun(ctx, claim.ID, claim.LeaseEpoch)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			logger.WithFields("eval_run_id", claim.ID).WithError(err).Warn("eval_runner: failed to load claimed run")
			r.releaseLeaseOnNonTerminalExit(claim.ID, claim.LeaseEpoch)
			continue
		}

		go func(run evalRunRow, leaseEpoch int64) {
			if err := r.processRun(ctx, run, leaseEpoch); err != nil {
				logger.WithFields("eval_run_id", run.ID).WithError(err).Warn("eval_runner: run processing failed")
			}
		}(run, claim.LeaseEpoch)
	}
}

type claimedEvalRun struct {
	ID         string `db:"id"`
	LeaseEpoch int64  `db:"lease_epoch"`
}

func (r *Runner) claimRuns(ctx context.Context) ([]claimedEvalRun, error) {
	ttlSeconds := int64(r.leaseTTL.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = int64(defaultEvalRunLeaseTTL.Seconds())
	}
	batch := r.claimBatch
	if batch < 1 {
		batch = defaultEvalRunClaimBatch
	}

	var claims []claimedEvalRun
	err := r.db.SelectContext(ctx, &claims, `
		UPDATE eval_runs
		SET lease_owner = $1,
			lease_expires_at = NOW() + ($2 * INTERVAL '1 second'),
			lease_epoch = lease_epoch + 1,
			status = 'running',
			started_at = COALESCE(started_at, NOW()),
			updated_at = NOW()
		WHERE id IN (
			SELECT id
			FROM eval_runs
			WHERE status IN ('pending','running')
				AND (lease_owner IS NULL OR lease_expires_at < NOW())
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		RETURNING id, lease_epoch
	`, r.workerID, ttlSeconds, batch)
	return claims, err
}

func (r *Runner) resetStaleRunningItems(ctx context.Context, runID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE eval_run_items
		SET status = 'pending', updated_at = NOW()
		WHERE eval_run_id = $1 AND status = 'running'
	`, runID)
	return err
}

func (r *Runner) getClaimedRun(ctx context.Context, runID string, leaseEpoch int64) (evalRunRow, error) {
	var run evalRunRow
	err := r.db.GetContext(ctx, &run, `
		SELECT
			id, tenant_id, dataset_id, COALESCE(dataset_version_id, '') AS dataset_version_id,
			status, eval_target_type, eval_target_id, eval_config, scorer_config_ids
		FROM eval_runs
		WHERE id = $1 AND lease_owner = $2 AND lease_epoch = $3 AND status = 'running'
	`, runID, r.workerID, leaseEpoch)
	return run, err
}

func (r *Runner) processRun(ctx context.Context, run evalRunRow, leaseEpoch int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := r.startLeaseHeartbeat(ctx, run.ID, leaseEpoch, cancel)
	defer stopHeartbeat()
	defer r.releaseLeaseOnNonTerminalExit(run.ID, leaseEpoch)

	status, err := r.getRunStatus(ctx, run.ID, run.TenantID)
	if err != nil {
		return err
	}
	if status == "cancelled" {
		return nil
	}
	if status == "pending" {
		if err := r.startRun(ctx, run.ID, run.TenantID); err != nil {
			return err
		}
	}

	if err := r.ensureRunItems(ctx, run); err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		status, err = r.getRunStatus(ctx, run.ID, run.TenantID)
		if err != nil {
			return err
		}
		if status == "cancelled" {
			return nil
		}

		items, err := r.listPendingItems(ctx, run)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return r.finalizeRun(ctx, run.ID, run.TenantID, leaseEpoch)
		}

		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := r.processItem(ctx, run, item, leaseEpoch); err != nil {
				if errors.Is(err, errEvalRunLeaseLost) {
					return err
				}
				logger.WithFields("eval_run_id", run.ID, "item_id", item.ID).WithError(err).Warn("eval_runner: item failed")
			}
		}

		if err := r.updateRunStats(ctx, run.ID, run.TenantID); err != nil {
			return err
		}
	}
}

func (r *Runner) startLeaseHeartbeat(ctx context.Context, runID string, leaseEpoch int64, cancel context.CancelFunc) func() {
	interval := r.leaseTTL / 3
	if interval < time.Second {
		interval = time.Second
	}
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := r.heartbeatLease(ctx, runID, leaseEpoch); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						cancel()
						return
					}
					if !errors.Is(err, context.Canceled) {
						logger.WithFields("eval_run_id", runID, "lease_epoch", leaseEpoch).WithError(err).Warn("eval_runner: lease heartbeat failed")
					}
				}
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

func (r *Runner) heartbeatLease(ctx context.Context, runID string, leaseEpoch int64) error {
	ttlSeconds := int64(r.leaseTTL.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = int64(defaultEvalRunLeaseTTL.Seconds())
	}

	var id string
	return r.db.GetContext(ctx, &id, `
		UPDATE eval_runs
		SET lease_expires_at = NOW() + ($4 * INTERVAL '1 second'),
			updated_at = NOW()
		WHERE id = $1 AND lease_owner = $2 AND lease_epoch = $3
		RETURNING id
	`, runID, r.workerID, leaseEpoch, ttlSeconds)
}

func (r *Runner) releaseLeaseOnNonTerminalExit(runID string, leaseEpoch int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := r.db.ExecContext(ctx, `
		UPDATE eval_runs
		SET lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = NOW()
		WHERE id = $1
			AND lease_epoch = $2
			AND status NOT IN ('completed','failed','cancelled')
	`, runID, leaseEpoch); err != nil {
		logger.WithFields("eval_run_id", runID, "lease_epoch", leaseEpoch).WithError(err).Warn("eval_runner: failed to release lease")
	}
}

func (r *Runner) getRunStatus(ctx context.Context, runID, tenantID string) (string, error) {
	var status string
	err := r.db.GetContext(ctx, &status, "SELECT status FROM eval_runs WHERE id = $1 AND tenant_id = $2", runID, tenantID)
	return status, err
}

func (r *Runner) startRun(ctx context.Context, runID, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE eval_runs
		SET status = 'running', started_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND status = 'pending'
	`, runID, tenantID)
	return err
}

func (r *Runner) ensureRunItems(ctx context.Context, run evalRunRow) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	type datasetItemID struct {
		ID    string `db:"id"`
		Input []byte `db:"input"`
	}
	var items []datasetItemID
	if run.DatasetVersionID != "" {
		if err := tx.SelectContext(ctx, &items, `
			SELECT COALESCE(source_dataset_item_id, id) AS id, input
			FROM dataset_version_items
			WHERE dataset_version_id = $1 AND tenant_id = $2
		`, run.DatasetVersionID, run.TenantID); err != nil {
			return err
		}
	} else {
		if err := tx.SelectContext(ctx, &items, `
			SELECT id, input
			FROM dataset_items
			WHERE dataset_id = $1 AND tenant_id = $2 AND status = 'active'
		`, run.DatasetID, run.TenantID); err != nil {
			return err
		}
	}

	now := time.Now()
	for _, item := range items {
		var canonicalParam interface{}
		var hashParam interface{}
		canonical, hash, err := CanonicalizeInput(item.Input)
		if err != nil {
			logger.WithFields("eval_run_id", run.ID, "dataset_item_id", item.ID).WithError(err).Warn("eval_runner: failed to canonicalize item input")
		} else {
			canonicalParam = string(canonical)
			hashParam = hash
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO eval_run_items (
				id, eval_run_id, dataset_item_id, tenant_id, status,
				input_canonical, input_hash, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'pending', $5::jsonb, $6, $7, $8)
			ON CONFLICT (eval_run_id, dataset_item_id) DO NOTHING
		`, uuid.New().String(), run.ID, item.ID, run.TenantID, canonicalParam, hashParam, now, now)
		if err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE eval_runs
		SET total_items = $2, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $3
	`, run.ID, len(items), run.TenantID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Runner) listPendingItems(ctx context.Context, run evalRunRow) ([]pendingItemRow, error) {
	var items []pendingItemRow
	if run.DatasetVersionID != "" {
		err := r.db.SelectContext(ctx, &items, `
			SELECT
				eri.id,
				eri.dataset_item_id,
				dvi.input,
				dvi.expected_output,
				dvi.source_trace_id,
				dvi.source_observation_id,
				dvi.metadata
			FROM eval_run_items eri
			JOIN dataset_version_items dvi
				ON COALESCE(dvi.source_dataset_item_id, dvi.id) = eri.dataset_item_id
				AND dvi.dataset_version_id = $2
				AND dvi.tenant_id = $3
			WHERE eri.eval_run_id = $1 AND eri.tenant_id = $3 AND eri.status = 'pending'
			ORDER BY eri.created_at ASC
			LIMIT 25
		`, run.ID, run.DatasetVersionID, run.TenantID)
		return items, err
	}

	err := r.db.SelectContext(ctx, &items, `
		SELECT
			eri.id,
			eri.dataset_item_id,
			di.input,
			di.expected_output,
			di.source_trace_id,
			di.source_observation_id,
			di.metadata
		FROM eval_run_items eri
		JOIN dataset_items di ON di.id = eri.dataset_item_id
		WHERE eri.eval_run_id = $1 AND eri.tenant_id = $2 AND eri.status = 'pending'
		ORDER BY eri.created_at ASC
		LIMIT 25
	`, run.ID, run.TenantID)
	return items, err
}

func (r *Runner) processItem(ctx context.Context, run evalRunRow, item pendingItemRow, leaseEpoch int64) error {
	// Tenant filter is mandatory even though the (id) match is unique —
	// it's the cheapest defense-in-depth against a hypothetical
	// projection bug or RLS bypass that ever lets two rows share an id
	// across tenants. Pairs with the runner's invariant that every row
	// it processes was selected with run.TenantID known.
	startRes, err := r.db.ExecContext(ctx, `
		UPDATE eval_run_items SET status = 'running', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND status = 'pending'
	`, item.ID, run.TenantID)
	if err != nil {
		return err
	}
	if rows, err := startRes.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return nil
	}

	var (
		output             interface{}
		traceID            string
		latencyMs          int64
		cost               float64
		tokenUsage         = map[string]interface{}{}
		scores             = map[string]interface{}{}
		errMsg             string
		status             = "completed"
		fromExpectedOutput bool
	)

	if run.EvalTargetType == "model" {
		modelOutput, modelUsage, modelLatencyMs, modelErr := r.runModelEval(ctx, run, item)
		if modelErr != nil {
			errMsg = modelErr.Error()
			status = "failed"
		} else {
			output = modelOutput
			tokenUsage = modelUsage
			latencyMs = modelLatencyMs
		}
	} else if item.SourceTraceID != "" && r.chConn != nil {
		traceID = item.SourceTraceID
		trace, traceErr := r.getTrace(ctx, traceID)
		if traceErr != nil {
			traceID = ""
		} else {
			if trace.TraceOutput != "" {
				if parsed, parseErr := parseJSON(trace.TraceOutput); parseErr == nil {
					output = parsed
				} else {
					output = map[string]interface{}{"raw": trace.TraceOutput}
				}
			} else {
				traceID = ""
			}
			latencyMs = trace.TotalDuration / int64(time.Millisecond)
			cost = trace.TotalCost
		}
	}

	if status != "failed" && output == nil && len(item.ExpectedOutput) > 0 {
		if parsed, parseErr := parseJSONBytes(item.ExpectedOutput); parseErr == nil {
			if m, ok := parsed.(map[string]interface{}); !ok || len(m) > 0 {
				output = parsed
				fromExpectedOutput = true
				if errMsg == "" && traceID == "" && item.SourceTraceID != "" {
					errMsg = "trace not found; used dataset expected_output"
				}
			}
		}
	}

	if status != "failed" && output == nil && len(item.Input) > 0 {
		if parsed, parseErr := parseJSONBytes(item.Input); parseErr == nil {
			if m, ok := parsed.(map[string]interface{}); !ok || len(m) > 0 {
				output = parsed
				if errMsg == "" && traceID == "" && item.SourceTraceID != "" {
					errMsg = "trace not found; used dataset input"
				}
			}
		}
	}

	if output == nil {
		if errMsg == "" {
			if item.SourceTraceID != "" {
				errMsg = "trace not found and no dataset output available"
			} else {
				errMsg = "no output available for evaluation"
			}
		}
		status = "failed"
	}

	if output != nil && len(item.ExpectedOutput) > 0 && !fromExpectedOutput {
		expected, _ := parseJSONBytes(item.ExpectedOutput)
		matched := reflect.DeepEqual(output, expected)
		matched = matchContentField(matched, output, expected)
		scores["exact_match"] = matched
	}

	// Run scorers if configured
	if len(run.ScorerConfigIDs) > 0 && status != "failed" {
		configs, loadErr := r.loadScoreConfigs(ctx, run.TenantID, run.ScorerConfigIDs)
		if loadErr != nil {
			logger.WithError(loadErr).Warn("eval_runner: failed to load score configs")
		} else {
			var parsedInput interface{}
			if len(item.Input) > 0 {
				parsedInput, _ = parseJSONBytes(item.Input)
			}
			var parsedExpected interface{}
			if len(item.ExpectedOutput) > 0 {
				parsedExpected, _ = parseJSONBytes(item.ExpectedOutput)
			}
			var parsedMeta interface{}
			if len(item.Metadata) > 0 {
				parsedMeta, _ = parseJSONBytes(item.Metadata)
			}

			// Retrieve RAG context if configured
			var retrievedContext string
			if r.retriever != nil {
				retrievalCfg := parseRetrievalConfig(run.EvalConfig)
				if retrievalCfg.Enabled {
					retrievedContext, _ = r.retriever.Retrieve(ctx, item, retrievalCfg)
				}
			}

			// Shared scorer dispatch (see score_output.go). The namespace scopes
			// any sandbox execution by run and lease epoch.
			scoreResults := r.ScoreOutput(ctx, run.TenantID, evalRunSandboxNamespace(run.ID, leaseEpoch), ScoreInput{
				Input:            parsedInput,
				Output:           output,
				ExpectedOutput:   parsedExpected,
				Metadata:         parsedMeta,
				RetrievedContext: retrievedContext,
			}, configs)
			for k, v := range scoreResults {
				scores[k] = v
			}
		}
	}

	outputJSON, _ := json.Marshal(output)
	tokenJSON, _ := json.Marshal(tokenUsage)
	scoresJSON, _ := json.Marshal(scores)

	return r.writeItemResult(ctx, run, item.ID, leaseEpoch, evalItemResult{
		status:     status,
		outputJSON: outputJSON,
		traceID:    traceID,
		latencyMs:  latencyMs,
		cost:       cost,
		tokenJSON:  tokenJSON,
		errMsg:     errMsg,
		scoresJSON: scoresJSON,
	})
}

func evalRunSandboxNamespace(runID string, leaseEpoch int64) string {
	return fmt.Sprintf("%s-e%d", runID, leaseEpoch)
}

type evalItemResult struct {
	status     string
	outputJSON []byte
	traceID    string
	latencyMs  int64
	cost       float64
	tokenJSON  []byte
	errMsg     string
	scoresJSON []byte
}

func (r *Runner) writeItemResult(ctx context.Context, run evalRunRow, itemID string, leaseEpoch int64, result evalItemResult) error {
	writeRes, err := r.db.ExecContext(ctx, `
		UPDATE eval_run_items
		SET status = $2, output = $3, trace_id = $4, latency_ms = $5, cost = $6,
			token_usage = $7, error = $8, scores = $9, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $10
			AND EXISTS (
				SELECT 1
				FROM eval_runs
				WHERE id = $11 AND lease_epoch = $12
			)
	`, itemID, result.status, result.outputJSON, result.traceID, result.latencyMs, result.cost, result.tokenJSON, result.errMsg, result.scoresJSON, run.TenantID, run.ID, leaseEpoch)
	if err != nil {
		return err
	}
	if rows, err := writeRes.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return errEvalRunLeaseLost
	}
	return nil
}

func (r *Runner) runModelEval(ctx context.Context, run evalRunRow, item pendingItemRow) (interface{}, map[string]interface{}, int64, error) {
	input, err := parseJSONBytes(item.Input)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to parse input: %w", err)
	}

	model := run.EvalTargetID
	if model == "" {
		if cfg := parseEvalConfig(run.EvalConfig); cfg != nil {
			if v, ok := cfg["model"].(string); ok && v != "" {
				model = v
			}
		}
	}
	if model == "" {
		if meta := parseJSONBytesNoErr(item.Metadata); meta != nil {
			if m, ok := meta.(map[string]interface{}); ok {
				if v, ok := m["model"].(string); ok && v != "" {
					model = v
				}
			}
		}
	}
	if model == "" {
		return nil, nil, 0, fmt.Errorf("model target not specified")
	}

	cfg := parseEvalConfig(run.EvalConfig)

	// When the eval config carries a prompt template (`prompt_messages`), render
	// it against the dataset item — substituting {{input}}/{{expected}} — instead
	// of deriving the request straight from the raw input. This is what lets a
	// playground "experiment" snapshot reproduce a task's system+user prompt
	// faithfully (see the frontend's applyRowTemplate).
	var reqPayload map[string]interface{}
	if tmpl := extractPromptMessages(cfg); tmpl != nil {
		expected := parseJSONBytesNoErr(item.ExpectedOutput)
		engine := "mustache"
		if e, ok := cfg["templating"].(string); ok && e != "" {
			engine = e
		}
		reqPayload = map[string]interface{}{
			"model":    model,
			"messages": renderPromptTemplate(tmpl, input, expected, engine),
		}
	} else {
		reqPayload = buildChatRequest(input, model)
	}
	if reqPayload == nil {
		return nil, nil, 0, fmt.Errorf("unable to build chat request from dataset input")
	}

	// Ensure non-streaming so we get a single JSON response
	reqPayload["stream"] = false

	if cfg != nil {
		mergeEvalConfig(reqPayload, cfg)
	}

	body, _ := json.Marshal(reqPayload)
	url := evalRunnerGatewayURL()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("MF_EVAL_RUNNER_API_KEY"); key != "" {
		httpReq.Header.Set("x-evs-api-key", key)
		httpReq.Header.Set("x-mf-api-key", key) // legacy alias (rolling-deploy safe)
	} else {
		// Internal service-to-service call — bypass gateway auth via same-origin indicator.
		// The HTTP middleware accepts this and marks the request as TenantAuthenticated;
		// the gRPC-gateway also forwards it so the Connect interceptor can accept it.
		internalauth.SetHeader(httpReq.Header)
	}
	// Always pass the run's tenant_id so the gateway's provider-config /
	// metrics path resolves to the correct tenant. Without this, the
	// downstream tenant-scoped lookups all hit "tenant context missing"
	// even though the request authenticates fine. Honored by the HTTP
	// api_key_interceptor only when paired with same-origin or a valid
	// API key, so this header alone isn't a tenant-spoof vector.
	if run.TenantID != "" {
		httpReq.Header.Set("x-tenant-id", run.TenantID)
	}
	// Group every trace produced by this eval under the experiment (its
	// dataset), so all runs over the same dataset share one session in the
	// observability Sessions view. Honored by the gateway's caller-supplied
	// session override (extractSessionID reads x-session-id).
	if run.DatasetID != "" {
		httpReq.Header.Set("x-session-id", run.DatasetID)
	}

	client := &http.Client{Timeout: 90 * time.Second}
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("model request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, 0, fmt.Errorf("model request failed: %s", string(respBody))
	}

	var respMap map[string]interface{}
	if err := json.Unmarshal(respBody, &respMap); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to parse model response: %w", err)
	}

	output := extractChatOutput(respMap)
	usage := extractTokenUsage(respMap)
	latencyMs := time.Since(start).Milliseconds()
	return output, usage, latencyMs, nil
}

func evalRunnerGatewayURL() string {
	if v := os.Getenv("MF_EVAL_RUNNER_GATEWAY_URL"); v != "" {
		return v
	}
	return "http://localhost:8089"
}

func buildChatRequest(input interface{}, model string) map[string]interface{} {
	switch v := input.(type) {
	case map[string]interface{}:
		if msgs, ok := v["messages"]; ok {
			return map[string]interface{}{"model": model, "messages": msgs}
		}
		if msg, ok := v["message"]; ok {
			return map[string]interface{}{"model": model, "messages": []interface{}{msg}}
		}
		if content, ok := v["content"]; ok {
			return map[string]interface{}{
				"model": model,
				"messages": []map[string]interface{}{
					{"role": "user", "content": content},
				},
			}
		}
		if prompt, ok := v["prompt"]; ok {
			return map[string]interface{}{
				"model": model,
				"messages": []map[string]interface{}{
					{"role": "user", "content": prompt},
				},
			}
		}
		if msgs, ok := v["input"]; ok {
			return map[string]interface{}{"model": model, "messages": msgs}
		}
		if text, ok := v["text"]; ok {
			return map[string]interface{}{
				"model": model,
				"messages": []map[string]interface{}{
					{"role": "user", "content": text},
				},
			}
		}
		if raw, ok := v["raw"]; ok {
			return map[string]interface{}{
				"model": model,
				"messages": []map[string]interface{}{
					{"role": "user", "content": raw},
				},
			}
		}
		// Last resort: if map has any content, serialize it as the user message
		if len(v) > 0 {
			data, _ := json.Marshal(v)
			return map[string]interface{}{
				"model": model,
				"messages": []map[string]interface{}{
					{"role": "user", "content": string(data)},
				},
			}
		}
	case []interface{}:
		return map[string]interface{}{
			"model":    model,
			"messages": v,
		}
	case string:
		return map[string]interface{}{
			"model": model,
			"messages": []map[string]interface{}{
				{"role": "user", "content": v},
			},
		}
	}
	return nil
}

func extractChatOutput(resp map[string]interface{}) interface{} {
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				return msg
			}
			if text, ok := choice["text"]; ok {
				return map[string]interface{}{"role": "assistant", "content": text}
			}
		}
	}
	return resp
}

func extractTokenUsage(resp map[string]interface{}) map[string]interface{} {
	if usage, ok := resp["usage"].(map[string]interface{}); ok {
		return usage
	}
	return map[string]interface{}{}
}

func parseEvalConfig(raw []byte) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func mergeEvalConfig(dst map[string]interface{}, cfg map[string]interface{}) {
	for k, v := range cfg {
		// Skip keys that are not gateway chat-completion params: the model is
		// applied separately, and prompt_messages/templating/messages are
		// prompt-template inputs consumed by renderPromptTemplate above.
		switch k {
		case "model", "prompt_messages", "templating", "messages":
			continue
		}
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
}

// extractPromptMessages returns the eval config's prompt template messages, or
// nil if none are configured.
func extractPromptMessages(cfg map[string]interface{}) []interface{} {
	if cfg == nil {
		return nil
	}
	if pm, ok := cfg["prompt_messages"].([]interface{}); ok && len(pm) > 0 {
		return pm
	}
	return nil
}

var promptVarRe = regexp.MustCompile(`\{\{\s*(input|expected)\s*\}\}`)

// renderPromptTemplate substitutes {{input}}/{{expected}} into each template
// message's content and returns chat-completion messages. Mirrors the
// playground's applyRowTemplate so a snapshot reproduces the task exactly.
// engine "none" disables substitution.
func renderPromptTemplate(msgs []interface{}, input, expected interface{}, engine string) []map[string]interface{} {
	inStr := templateText(input)
	expStr := templateText(expected)
	out := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		if role == "" {
			role = "user"
		}
		content, _ := mm["content"].(string)
		if engine != "none" {
			content = promptVarRe.ReplaceAllStringFunc(content, func(tok string) string {
				if strings.Contains(tok, "expected") {
					return expStr
				}
				return inStr
			})
		}
		out = append(out, map[string]interface{}{"role": role, "content": content})
	}
	return out
}

// templateText derives a string form of a dataset item field for {{var}}
// substitution. Mirrors the frontend's structToText: unwrap common single-value
// shapes, else JSON-encode.
func templateText(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case map[string]interface{}:
		for _, k := range []string{"input", "value", "text", "output", "expected", "expected_output"} {
			if val, ok := t[k]; ok {
				return templateText(val)
			}
		}
		if len(t) == 1 {
			for _, val := range t {
				return templateText(val)
			}
		}
		b, _ := json.Marshal(t)
		return string(b)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func parseJSONBytesNoErr(raw []byte) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func matchContentField(current bool, output interface{}, expected interface{}) bool {
	if current {
		return true
	}
	outMap, ok := output.(map[string]interface{})
	if !ok {
		return false
	}
	expMap, ok := expected.(map[string]interface{})
	if !ok {
		return false
	}
	oc, ok := outMap["content"]
	if !ok {
		return false
	}
	ec, ok := expMap["content"]
	if !ok {
		return false
	}
	return reflect.DeepEqual(oc, ec)
}

func (r *Runner) updateRunStats(ctx context.Context, runID, tenantID string) error {
	var total, completed, failed int
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'completed') as completed,
			COUNT(*) FILTER (WHERE status = 'failed') as failed
		FROM eval_run_items
		WHERE eval_run_id = $1 AND tenant_id = $2
	`, runID, tenantID).Scan(&total, &completed, &failed)
	if err != nil {
		return err
	}

	scoreSummary, err := r.buildScoreSummary(ctx, runID, completed)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE eval_runs
		SET total_items = $2, completed_items = $3, failed_items = $4,
			score_summary = $5, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $6
	`, runID, total, completed, failed, scoreSummary, tenantID)
	return err
}

func (r *Runner) finalizeRun(ctx context.Context, runID, tenantID string, leaseEpoch int64) error {
	var pending int
	if err := r.db.GetContext(ctx, &pending, `
		SELECT COUNT(*) FROM eval_run_items
		WHERE eval_run_id = $1 AND tenant_id = $2 AND status IN ('pending','running')
	`, runID, tenantID); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}

	var failed int
	if err := r.db.GetContext(ctx, &failed, `
		SELECT COUNT(*) FROM eval_run_items
		WHERE eval_run_id = $1 AND tenant_id = $2 AND status = 'failed'
	`, runID, tenantID); err != nil {
		return err
	}

	finalStatus := "completed"
	if failed > 0 {
		finalStatus = "failed"
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE eval_runs
		SET status = $2,
			completed_at = NOW(),
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = NOW()
		WHERE id = $1
			AND tenant_id = $3
			AND lease_epoch = $4
			AND status NOT IN ('completed','failed','cancelled')
	`, runID, finalStatus, tenantID, leaseEpoch)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return nil
	}

	// Clean up sandboxes for this run after this caller wins the terminal
	// transition; stale finalizers must not reap the current owner's sandboxes.
	if r.sandboxScorer != nil {
		r.sandboxScorer.DestroyRunSandboxes(ctx, runID)
	}

	// Run regression detection against baseline (best-effort, don't fail the run)
	if finalStatus == "completed" {
		if regResult, regErr := CheckRegression(ctx, r.db, runID); regErr != nil {
			logger.WithError(regErr).Warn("regression check failed for run ", runID)
		} else if regResult != nil {
			if storeErr := StoreRegressionResult(ctx, r.db, runID, regResult); storeErr != nil {
				logger.WithError(storeErr).Warn("failed to store regression result for run ", runID)
			}
			if regResult.HasRegression {
				logger.Warn("regression detected in eval run ", runID, " vs baseline ", regResult.BaselineRunID)
				if r.regNotifier != nil {
					// Fetch tenant ID for the alert
					var tenantID string
					if tErr := r.db.GetContext(ctx, &tenantID, `SELECT tenant_id FROM eval_runs WHERE id = $1`, runID); tErr == nil {
						r.regNotifier.NotifyRegression(ctx, tenantID, runID, regResult)
					}
				}
			}
		}
	}

	return nil
}

func (r *Runner) getTrace(ctx context.Context, traceID string) (*query.TraceReadModel, error) {
	if r.chConn == nil {
		return nil, fmt.Errorf("clickhouse unavailable")
	}
	handler := traceshandler.NewGetTraceHandler(r.chConn)
	q := traceshandler.NewGetTraceQuery(traceID, "", "")
	res, err := handler.Handle(ctx, q)
	if err != nil {
		return nil, err
	}
	trace, ok := res.(*query.TraceReadModel)
	if !ok || trace == nil {
		return nil, fmt.Errorf("trace not found")
	}
	return trace, nil
}

type scoreSummaryRow struct {
	Key      string  `db:"key"`
	AvgScore float64 `db:"avg_score"`
	MinScore float64 `db:"min_score"`
	MaxScore float64 `db:"max_score"`
	Count    int     `db:"count"`
}

func (r *Runner) buildScoreSummary(ctx context.Context, runID string, completed int) ([]byte, error) {
	if completed == 0 {
		return []byte("{}"), nil
	}

	var rows []scoreSummaryRow
	err := r.db.SelectContext(ctx, &rows, `
		SELECT
			s.key,
			AVG(
				CASE
					WHEN s.value::text = 'true' THEN 1.0
					WHEN s.value::text = 'false' THEN 0.0
					ELSE (s.value#>>'{}')::float
				END
			) AS avg_score,
			MIN(
				CASE
					WHEN s.value::text = 'true' THEN 1.0
					WHEN s.value::text = 'false' THEN 0.0
					ELSE (s.value#>>'{}')::float
				END
			) AS min_score,
			MAX(
				CASE
					WHEN s.value::text = 'true' THEN 1.0
					WHEN s.value::text = 'false' THEN 0.0
					ELSE (s.value#>>'{}')::float
				END
			) AS max_score,
			COUNT(*) AS count
		FROM eval_run_items,
			jsonb_each(scores) AS s(key, value)
		WHERE eval_run_id = $1
			AND status = 'completed'
			AND s.key NOT LIKE '%\_error'
			AND s.key NOT LIKE '%\_reason'
		GROUP BY s.key
	`, runID)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return []byte("{}"), nil
	}

	scores := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		scores = append(scores, map[string]interface{}{
			"name":     row.Key,
			"avgScore": row.AvgScore,
			"minScore": row.MinScore,
			"maxScore": row.MaxScore,
			"count":    row.Count,
		})
	}

	payload := map[string]interface{}{
		"scores": scores,
	}
	return json.Marshal(payload)
}

func parseJSON(raw string) (interface{}, error) {
	var out interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseJSONBytes(raw []byte) (interface{}, error) {
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
