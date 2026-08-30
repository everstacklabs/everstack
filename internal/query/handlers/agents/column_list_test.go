package agents

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx/reflectx"
)

// The agents read path must never go back to `SELECT *`. sqlx fails the whole
// scan when a returned column has no destination field, so a `SELECT *` here
// turns any additive migration that reaches the database ahead of the binary
// into `missing destination name <column>` on every agent query — which is
// exactly what a rolling deploy produces, and what took out the dev agents
// pages after agent_revisions_20260806000000 landed.

func TestSelectListDerivesColumnsFromDBTags(t *testing.T) {
	type model struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
		Skipped  string `db:"-"`
		Untagged string
	}

	if got, want := selectList(model{}, ""), "id, tenant_id"; got != want {
		t.Fatalf("unaliased: got %q, want %q", got, want)
	}
	if got, want := selectList(model{}, "s"), "s.id, s.tenant_id"; got != want {
		t.Fatalf("aliased: got %q, want %q", got, want)
	}
	if got, want := selectList(&model{}, ""), "id, tenant_id"; got != want {
		t.Fatalf("pointer: got %q, want %q", got, want)
	}
}

// agent_sessions has no `summary` column — ListSessions computes it from the
// session's first turn and aliases it in. Selecting it by name would make
// every session query fail with `column "summary" does not exist`.
func TestSessionColumnsOmitComputedSummary(t *testing.T) {
	for name, list := range map[string]string{
		"unaliased": sessionColumns,
		"aliased":   sessionColumnsS,
	} {
		for _, field := range strings.Split(list, ", ") {
			if field == "summary" || field == "s.summary" {
				t.Fatalf("%s column list selects the computed summary column: %q", name, list)
			}
		}
	}
}

// Every read model must contribute a non-empty list; an empty one would render
// `SELECT  FROM ...` and fail at the database.
func TestColumnListsAreNonEmpty(t *testing.T) {
	for name, list := range map[string]string{
		"agentColumns":          agentColumns,
		"sessionColumns":        sessionColumns,
		"sessionColumnsS":       sessionColumnsS,
		"sessionTurnColumns":    sessionTurnColumns,
		"approvalReviewColumns": approvalReviewColumns,
		"spawnTreeColumns":      spawnTreeColumns,
		"agentLinkColumns":      agentLinkColumns,
		"channelBindingColumns": channelBindingColumns,
	} {
		if strings.TrimSpace(list) == "" {
			t.Fatalf("%s is empty", name)
		}
	}
}

// The tenant filter is enforced by column-independent WHERE clauses, but the
// read models still carry tenant_id and the FE relies on it being scanned.
func TestTenantScopedModelsSelectTenantID(t *testing.T) {
	for name, list := range map[string]string{
		"agentColumns":          agentColumns,
		"sessionColumns":        sessionColumns,
		"approvalReviewColumns": approvalReviewColumns,
		"spawnTreeColumns":      spawnTreeColumns,
		"agentLinkColumns":      agentLinkColumns,
		"channelBindingColumns": channelBindingColumns,
	} {
		if !selectsColumn(list, "tenant_id") {
			t.Fatalf("%s does not select tenant_id: %q", name, list)
		}
	}
}

// selectList walks top-level `db` tags. sqlx maps more than that — untagged
// fields by lowercased name, and fields promoted from embedded structs — so a
// list built by hand-rolled reflection can silently omit a column sqlx would
// have scanned, leaving that field zero forever with no error to notice. Hold
// the two mappers to the same set so adding a field in a shape selectList does
// not walk fails here instead of in production.
func TestColumnListsMatchSqlxMapper(t *testing.T) {
	// The mapper sqlx installs by default (see sqlx.NewDb).
	mapper := reflectx.NewMapperFunc("db", strings.ToLower)

	for name, tc := range map[string]struct {
		model interface{}
		list  string
	}{
		"AgentDefinitionReadModel":     {AgentDefinitionReadModel{}, agentColumns},
		"AgentSessionReadModel":        {AgentSessionReadModel{}, sessionColumns},
		"AgentSessionTurnReadModel":    {AgentSessionTurnReadModel{}, sessionTurnColumns},
		"ApprovalReviewReadModel":      {ApprovalReviewReadModel{}, approvalReviewColumns},
		"SpawnTreeNodeReadModel":       {SpawnTreeNodeReadModel{}, spawnTreeColumns},
		"AgentLinkReadModel":           {AgentLinkReadModel{}, agentLinkColumns},
		"AgentChannelBindingReadModel": {AgentChannelBindingReadModel{}, channelBindingColumns},
	} {
		virtual := map[string]bool{}
		if v, ok := tc.model.(virtualColumner); ok {
			for _, c := range v.virtualColumns() {
				virtual[c] = true
			}
		}

		var scannable []string
		for path := range mapper.TypeMap(reflect.TypeOf(tc.model)).Names {
			// Embedded structs register their own path as well as the
			// promoted leaves; only leaves are scannable columns.
			if strings.Contains(path, ".") || virtual[path] {
				continue
			}
			scannable = append(scannable, path)
		}

		selected := strings.Split(tc.list, ", ")
		sort.Strings(scannable)
		sort.Strings(selected)

		if !reflect.DeepEqual(scannable, selected) {
			t.Errorf("%s: column list drifted from the sqlx mapping\n  sqlx scans: %v\n  we select:  %v", name, scannable, selected)
		}
	}
}

func selectsColumn(list, want string) bool {
	for _, field := range strings.Split(list, ", ") {
		if field == want {
			return true
		}
	}
	return false
}
