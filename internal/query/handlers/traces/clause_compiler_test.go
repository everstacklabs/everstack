package traces

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

func TestClauseCompilerFields(t *testing.T) {
	tenantID := "tenant-clause-fields"
	tenantClause, _ := tenantBridgeFilter(tenantID)
	wantAny := func(predicate string) string {
		return fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s)", tenantClause, predicate)
	}

	tests := []struct {
		name          string
		clause        TraceFilterClause
		wantCondition string
		wantArgs      []any
	}{
		{
			name:          "model",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "model", Op: OpEq, Value: "gpt-4o"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", modelSQL())),
			wantArgs:      []any{tenantID, tenantID, "gpt-4o"},
		},
		{
			name:          "provider",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "provider", Op: OpEq, Value: "openai"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", providerSQL())),
			wantArgs:      []any{tenantID, tenantID, "openai"},
		},
		{
			name:          "user",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "user", Op: OpEq, Value: "user-1"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", userSQL())),
			wantArgs:      []any{tenantID, tenantID, "user-1"},
		},
		{
			name:          "session",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "session", Op: OpEq, Value: "session-1"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", sessionSQL())),
			wantArgs:      []any{tenantID, tenantID, "session-1"},
		},
		{
			name:          "thread",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "thread", Op: OpEq, Value: "thread-1"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", threadSQL())),
			wantArgs:      []any{tenantID, tenantID, "thread-1"},
		},
		{
			name:          "correlation",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "correlation", Op: OpEq, Value: "corr-1"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", correlationSQL())),
			wantArgs:      []any{tenantID, tenantID, "corr-1"},
		},
		// otel_traces stores a span status as "Ok"/"Error"/"Unset" from the
		// collector and as the enum name from the Everstack OTLP handler.
		// Binding one literal to `StatusCode = ?` matched neither reliably,
		// so a status filter has to compile to a membership test.
		{
			name:          "status OK",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "status", Op: OpEq, Value: "OK"},
			wantCondition: wantAny(otelstatus.IsOK(otelstatus.Column)),
			wantArgs:      []any{tenantID, tenantID},
		},
		{
			name:          "status ERROR",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "status", Op: OpEq, Value: "ERROR"},
			wantCondition: wantAny(otelstatus.IsError(otelstatus.Column)),
			wantArgs:      []any{tenantID, tenantID},
		},
		{
			name:          "status not ERROR keeps unset spans",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "status", Op: OpNe, Value: "ERROR"},
			wantCondition: wantAny(otelstatus.IsNotError(otelstatus.Column)),
			wantArgs:      []any{tenantID, tenantID},
		},
		{
			name:          "status not OK drops unset spans",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "status", Op: OpNe, Value: "OK"},
			wantCondition: wantAny(otelstatus.IsNotOK(otelstatus.Column)),
			wantArgs:      []any{tenantID, tenantID},
		},
		{
			name:          "tool name",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "tool.name", Op: OpEq, Value: "web_search"},
			wantCondition: wantAny(fmt.Sprintf("%s = ?", toolNameSQL())),
			wantArgs:      []any{tenantID, tenantID, "web_search"},
		},
		{
			name:          "tokens total numeric",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "tokens.total", Op: OpGte, Value: "42"},
			wantCondition: wantAny(fmt.Sprintf("toInt64OrZero(%s) >= ?", totalTokensSQL())),
			wantArgs:      []any{tenantID, tenantID, int64(42)},
		},
		{
			name:          "tokens alias numeric",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "tokens", Op: OpLt, Value: "100"},
			wantCondition: wantAny(fmt.Sprintf("toInt64OrZero(%s) < ?", totalTokensSQL())),
			wantArgs:      []any{tenantID, tenantID, int64(100)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conditions, args, err := compileClauses([]TraceFilterClause{tc.clause}, tenantID)
			if err != nil {
				t.Fatalf("compileClauses returned error: %v", err)
			}
			if !reflect.DeepEqual(conditions, []string{tc.wantCondition}) {
				t.Fatalf("conditions = %#v, want %#v", conditions, []string{tc.wantCondition})
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, tc.wantArgs)
			}
		})
	}
}

func TestClauseCompilerScopeAnyAndRoot(t *testing.T) {
	tenantID := "tenant-clause-scope"
	tenantClause, _ := tenantBridgeFilter(tenantID)
	predicate := fmt.Sprintf("%s = ?", modelSQL())

	tests := []struct {
		name          string
		scope         ClauseScope
		wantCondition string
	}{
		{
			name:          "any",
			scope:         ScopeAny,
			wantCondition: fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s)", tenantClause, predicate),
		},
		{
			name:          "root",
			scope:         ScopeRoot,
			wantCondition: fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND ParentSpanId = '' AND %s)", tenantClause, predicate),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conditions, args, err := compileClauses([]TraceFilterClause{{
				Scope: tc.scope,
				Field: "model",
				Op:    OpEq,
				Value: "gpt-4o",
			}}, tenantID)
			if err != nil {
				t.Fatalf("compileClauses returned error: %v", err)
			}
			if !reflect.DeepEqual(conditions, []string{tc.wantCondition}) {
				t.Fatalf("conditions = %#v, want %#v", conditions, []string{tc.wantCondition})
			}
			if wantArgs := []any{tenantID, tenantID, "gpt-4o"}; !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, wantArgs)
			}
		})
	}
}

func TestClauseCompilerExists(t *testing.T) {
	tenantID := "tenant-clause-exists"
	tenantClause, _ := tenantBridgeFilter(tenantID)
	wantCondition := fmt.Sprintf(
		"TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s != '')",
		tenantClause,
		modelSQL(),
	)

	conditions, args, err := compileClauses([]TraceFilterClause{{
		Scope: ScopeAny,
		Field: "model",
		Op:    OpExists,
	}}, tenantID)
	if err != nil {
		t.Fatalf("compileClauses returned error: %v", err)
	}
	if !reflect.DeepEqual(conditions, []string{wantCondition}) {
		t.Fatalf("conditions = %#v, want %#v", conditions, []string{wantCondition})
	}
	if wantArgs := []any{tenantID, tenantID}; !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestClauseCompilerMetadata(t *testing.T) {
	tenantID := "tenant-clause-metadata"
	tenantClause, _ := tenantBridgeFilter(tenantID)
	wantCondition := fmt.Sprintf(
		"TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND SpanAttributes[?] = ?)",
		tenantClause,
	)

	conditions, args, err := compileClauses([]TraceFilterClause{{
		Scope: ScopeAny,
		Field: "metadata.app.version",
		Op:    OpEq,
		Value: "2026.07",
	}}, tenantID)
	if err != nil {
		t.Fatalf("compileClauses returned error: %v", err)
	}
	if !reflect.DeepEqual(conditions, []string{wantCondition}) {
		t.Fatalf("conditions = %#v, want %#v", conditions, []string{wantCondition})
	}
	if wantArgs := []any{tenantID, tenantID, "app.version", "2026.07"}; !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestClauseCompilerRejectsUnsafeMetadataKey(t *testing.T) {
	_, _, err := compileClauses([]TraceFilterClause{{
		Scope: ScopeAny,
		Field: "metadata.bad key",
		Op:    OpEq,
		Value: "x",
	}}, "tenant-bad-metadata")
	if err == nil {
		t.Fatal("compileClauses returned nil error for unsafe metadata key")
	}
	if !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("error = %q, want it to name the unsafe key", err.Error())
	}
}

func TestClauseCompilerUnknownField(t *testing.T) {
	_, _, err := compileClauses([]TraceFilterClause{{
		Scope: ScopeAny,
		Field: "does.not.exist",
		Op:    OpEq,
		Value: "x",
	}}, "tenant-unknown-field")
	if err == nil {
		t.Fatal("compileClauses returned nil error for unknown field")
	}
	if !strings.Contains(err.Error(), "does.not.exist") {
		t.Fatalf("error = %q, want it to name the unknown field", err.Error())
	}
}

func TestClauseCompilerEverySubqueryContainsTenantClause(t *testing.T) {
	tenantID := "tenant-security-invariant"
	tenantClause, _ := tenantBridgeFilter(tenantID)

	conditions, args, err := compileClauses([]TraceFilterClause{
		{Scope: ScopeAny, Field: "model", Op: OpEq, Value: "gpt-4o"},
		{Scope: ScopeRoot, Field: "provider", Op: OpEq, Value: "openai"},
		{Scope: ScopeAny, Field: "metadata.env", Op: OpEq, Value: "prod"},
	}, tenantID)
	if err != nil {
		t.Fatalf("compileClauses returned error: %v", err)
	}
	for i, condition := range conditions {
		if !strings.Contains(condition, tenantClause) {
			t.Fatalf("condition %d = %q, want tenant clause substring %q", i, condition, tenantClause)
		}
	}
	wantArgs := []any{
		tenantID, tenantID, "gpt-4o",
		tenantID, tenantID, "openai",
		tenantID, tenantID, "env", "prod",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestClauseCompilerRejectsNonNumericTokenValue(t *testing.T) {
	_, _, err := compileClauses([]TraceFilterClause{{
		Scope: ScopeAny,
		Field: "tokens.total",
		Op:    OpGt,
		Value: "a lot",
	}}, "tenant-bad-number")
	if err == nil {
		t.Fatal("compileClauses returned nil error for non-numeric tokens.total")
	}
	if !strings.Contains(err.Error(), "tokens.total") {
		t.Fatalf("error = %q, want it to name tokens.total", err.Error())
	}
}

func TestClauseCompilerBespokeFields(t *testing.T) {
	tenantID := "tenant-bespoke"
	tenantClause, _ := tenantBridgeFilter(tenantID)
	wantAny := func(predicate string) string {
		return fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s)", tenantClause, predicate)
	}

	tests := []struct {
		name          string
		clause        TraceFilterClause
		wantCondition string
		wantArgs      []any
	}{
		{
			name:          "tool.error exists",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "tool.error", Op: OpExists},
			wantCondition: wantAny(toolErrorExistsSQL()),
			wantArgs:      []any{tenantID, tenantID},
		},
		{
			name:          "cache.hit exists",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "cache.hit", Op: OpExists},
			wantCondition: wantAny(cacheHitExistsSQL()),
			wantArgs:      []any{tenantID, tenantID},
		},
		{
			name:          "ttft numeric",
			clause:        TraceFilterClause{Scope: ScopeAny, Field: "ttft", Op: OpGt, Value: "5000"},
			wantCondition: wantAny(fmt.Sprintf("toInt64OrZero(%s) > ?", ttftSQL())),
			wantArgs:      []any{tenantID, tenantID, int64(5000)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conds, args, err := compileClauses([]TraceFilterClause{tt.clause}, tenantID)
			if err != nil {
				t.Fatalf("compileClauses: %v", err)
			}
			if len(conds) != 1 || conds[0] != tt.wantCondition {
				t.Fatalf("condition\n got %q\nwant %q", strings.Join(conds, " | "), tt.wantCondition)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Fatalf("args got %#v want %#v", args, tt.wantArgs)
			}
		})
	}
}

func TestClauseCompilerHasAndAgent(t *testing.T) {
	tenantID := "tenant-has"
	tenantClause, _ := tenantBridgeFilter(tenantID)
	wantAny := func(p string) string {
		return fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s)", tenantClause, p)
	}

	t.Run("has sandbox -> observation.type", func(t *testing.T) {
		conds, args, err := compileClauses([]TraceFilterClause{{Scope: ScopeAny, Field: "has", Op: OpEq, Value: "sandbox"}}, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		if want := wantAny("SpanAttributes['observation.type'] = 'SANDBOX'"); conds[0] != want {
			t.Fatalf("got %q want %q", conds[0], want)
		}
		if !reflect.DeepEqual(args, []any{tenantID, tenantID}) {
			t.Fatalf("args %#v", args)
		}
	})

	t.Run("has voice maps to MEDIA", func(t *testing.T) {
		conds, _, err := compileClauses([]TraceFilterClause{{Field: "has", Op: OpEq, Value: "voice"}}, tenantID)
		if err != nil || !strings.Contains(conds[0], "'MEDIA'") {
			t.Fatalf("conds=%v err=%v", conds, err)
		}
	})

	t.Run("has unknown value fails closed", func(t *testing.T) {
		if _, _, err := compileClauses([]TraceFilterClause{{Field: "has", Op: OpEq, Value: "bogus"}}, tenantID); err == nil {
			t.Fatal("want error for unknown has value")
		}
	})

	t.Run("agent name", func(t *testing.T) {
		conds, args, err := compileClauses([]TraceFilterClause{{Scope: ScopeAny, Field: "agent", Op: OpEq, Value: "researcher"}}, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		if want := wantAny(fmt.Sprintf("%s = ?", agentNameSQL())); conds[0] != want {
			t.Fatalf("got %q want %q", conds[0], want)
		}
		if !reflect.DeepEqual(args, []any{tenantID, tenantID, "researcher"}) {
			t.Fatalf("args %#v", args)
		}
	})
}
