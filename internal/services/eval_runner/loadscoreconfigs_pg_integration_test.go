package eval_runner

import (
	"context"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // same driver as production (supports []string -> ANY($2))
	"github.com/jmoiron/sqlx"
)

// TestLoadScoreConfigs_PGRoundTrip exercises the real FE→BE→DB read hop against
// Postgres: a projection-shaped choice-scorer row (JSONB messages/model_params/
// choice_scores) must load back into the typed ScoreConfig intact, and loading
// must stay tenant-scoped. Gated on EVAL_RUNNER_PG_DSN so `go test` stays hermetic.
//
//	EVAL_RUNNER_PG_DSN='postgres://postgres:test@localhost:5432/testdb?sslmode=disable' \
//	  go test ./internal/services/eval_runner/ -run PGRoundTrip -v
func TestLoadScoreConfigs_PGRoundTrip(t *testing.T) {
	dsn := os.Getenv("EVAL_RUNNER_PG_DSN")
	if dsn == "" {
		t.Skip("set EVAL_RUNNER_PG_DSN to run the Postgres round-trip test")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Fresh table with the real base schema + the migration's added columns.
	_, err = db.ExecContext(ctx, `
		DROP TABLE IF EXISTS score_configs;
		CREATE TABLE score_configs (
			id VARCHAR(255) PRIMARY KEY,
			tenant_id VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			data_type VARCHAR(50) NOT NULL,
			description TEXT DEFAULT '',
			min_value DOUBLE PRECISION,
			max_value DOUBLE PRECISION,
			categories JSONB,
			eval_prompt TEXT DEFAULT '',
			eval_model VARCHAR(255) DEFAULT '',
			is_archived BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			scorer_code TEXT DEFAULT '',
			scorer_language VARCHAR(50) DEFAULT '',
			use_sandbox BOOLEAN NOT NULL DEFAULT false,
			slug VARCHAR(255) NOT NULL DEFAULT '',
			scorer_type VARCHAR(50) NOT NULL DEFAULT '',
			output_type VARCHAR(50) NOT NULL DEFAULT '',
			messages JSONB NOT NULL DEFAULT '[]',
			model_params JSONB NOT NULL DEFAULT '{}',
			choice_scores JSONB NOT NULL DEFAULT '[]',
			use_cot BOOLEAN NOT NULL DEFAULT false,
			pass_threshold DOUBLE PRECISION
		);
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Row shaped exactly as the projection writes a Factuality-style choice scorer.
	_, err = db.ExecContext(ctx, `
		INSERT INTO score_configs
			(id, tenant_id, name, data_type, eval_model, slug, scorer_type, output_type,
			 messages, model_params, choice_scores, use_cot, pass_threshold)
		VALUES
			('cfg-1','t1','Factuality','LLM_JUDGE','gpt-5','factuality','llm_judge','choice',
			 '[{"role":"user","content":"Compare {{output}} to {{expected_output}}"}]',
			 '{"temperature":0.2,"top_p":0.9,"max_tokens":256}',
			 '[{"choice":"A","score":0.4},{"choice":"B","score":0.6},{"choice":"C","score":1}]',
			 true, 0.5);
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	r := &Runner{db: db}
	cfgs, err := r.loadScoreConfigs(ctx, "t1", []string{"cfg-1"})
	if err != nil {
		t.Fatalf("loadScoreConfigs: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(cfgs))
	}
	c := cfgs[0]

	if c.ScorerType != "llm_judge" || c.OutputType != "choice" || c.Slug != "factuality" {
		t.Fatalf("scalar fields wrong: %+v", c)
	}
	if len(c.Messages) != 1 || c.Messages[0].Role != "user" ||
		c.Messages[0].Content != "Compare {{output}} to {{expected_output}}" {
		t.Fatalf("messages did not round-trip: %#v", c.Messages)
	}
	if c.ModelParams.Temperature == nil || *c.ModelParams.Temperature != 0.2 ||
		c.ModelParams.MaxTokens == nil || *c.ModelParams.MaxTokens != 256 {
		t.Fatalf("model_params did not round-trip: %#v", c.ModelParams)
	}
	if len(c.ChoiceScores) != 3 || c.ChoiceScores[0].Choice != "A" || c.ChoiceScores[0].Score != 0.4 {
		t.Fatalf("choice_scores did not round-trip: %#v", c.ChoiceScores)
	}
	if !c.UseCot || c.PassThreshold == nil || *c.PassThreshold != 0.5 {
		t.Fatalf("use_cot/pass_threshold wrong: use_cot=%v thr=%v", c.UseCot, c.PassThreshold)
	}

	// Verify the choice actually maps to a score end to end.
	if v, _, err := parseScoreResponse(`{"choice":"B","reasoning":"x"}`, c); err != nil || v.(float64) != 0.6 {
		t.Fatalf("choice B should map to 0.6 for the loaded config: v=%v err=%v", v, err)
	}

	// Tenant scoping: another tenant must not see this config.
	other, err := r.loadScoreConfigs(ctx, "t2", []string{"cfg-1"})
	if err != nil {
		t.Fatalf("loadScoreConfigs t2: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("tenant scoping breach: t2 loaded %d configs", len(other))
	}
}
