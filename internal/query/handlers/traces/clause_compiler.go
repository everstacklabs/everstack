package traces

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

type ClauseScope string

const (
	ScopeAny  ClauseScope = "any"
	ScopeRoot ClauseScope = "root"
)

type ClauseOp string

const (
	OpEq     ClauseOp = "="
	OpNe     ClauseOp = "!="
	OpGt     ClauseOp = ">"
	OpGte    ClauseOp = ">="
	OpLt     ClauseOp = "<"
	OpLte    ClauseOp = "<="
	OpExists ClauseOp = "exists"
)

type TraceFilterClause struct {
	Scope ClauseScope
	Field string
	Op    ClauseOp
	Value string
}

type clauseFieldKind int

const (
	clauseFieldString clauseFieldKind = iota
	clauseFieldNumber
	clauseFieldStatus
)

const metadataFieldPrefix = "metadata."

// compileClauses turns span-scoped trace filter clauses into trace-membership
// subqueries. Every subquery is tenant-scoped independently.
func compileClauses(clauses []TraceFilterClause, tenantID string) (conditions []string, args []any, err error) {
	conditions = make([]string, 0, len(clauses))
	args = make([]any, 0, len(clauses)*3)

	for _, clause := range clauses {
		scope, err := normalizeClauseScope(clause.Scope)
		if err != nil {
			return nil, nil, err
		}

		predicate, predicateArgs, err := compileClausePredicate(clause)
		if err != nil {
			return nil, nil, err
		}

		tenantClause, tenantArgs := tenantBridgeFilter(tenantID)
		subqueryParts := []string{tenantClause}
		if scope == ScopeRoot {
			subqueryParts = append(subqueryParts, "ParentSpanId = ''")
		}
		subqueryParts = append(subqueryParts, predicate)

		conditions = append(conditions, fmt.Sprintf(
			"TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s)",
			strings.Join(subqueryParts, " AND "),
		))
		args = append(args, tenantArgs...)
		args = append(args, predicateArgs...)
	}

	return conditions, args, nil
}

func normalizeClauseScope(scope ClauseScope) (ClauseScope, error) {
	switch scope {
	case "", ScopeAny:
		return ScopeAny, nil
	case ScopeRoot:
		return ScopeRoot, nil
	default:
		return "", fmt.Errorf("unknown trace filter scope %q", scope)
	}
}

func compileClausePredicate(clause TraceFilterClause) (string, []any, error) {
	if strings.HasPrefix(clause.Field, metadataFieldPrefix) {
		return compileMetadataClausePredicate(clause)
	}

	// "has:<span-type>" -> trace contains a span of that kind. The value maps to
	// a FIXED condition (never interpolated), so it is injection-safe.
	if clause.Field == "has" {
		cond, ok := hasSpanCondition(clause.Value)
		if !ok {
			return "", nil, fmt.Errorf("unknown span type for has: %q", clause.Value)
		}
		return cond, nil, nil
	}

	// Fields with bespoke boolean-exists semantics (a condition, not a plain
	// attribute presence): "tool.error exists" means a tool call failed.
	if clause.Op == OpExists {
		switch clause.Field {
		case "tool.error":
			return toolErrorExistsSQL(), nil, nil
		case "cache.hit":
			return cacheHitExistsSQL(), nil, nil
		}
	}

	expr, kind, err := traceClauseFieldExpr(clause.Field)
	if err != nil {
		return "", nil, err
	}

	if clause.Op == OpExists {
		switch kind {
		case clauseFieldNumber:
			return fmt.Sprintf("%s != 0", expr), nil, nil
		default:
			return fmt.Sprintf("%s != ''", expr), nil, nil
		}
	}

	sqlOp, err := clauseSQLOp(clause.Op)
	if err != nil {
		return "", nil, err
	}

	switch kind {
	case clauseFieldNumber:
		value, err := strconv.ParseInt(clause.Value, 10, 64)
		if err != nil {
			return "", nil, fmt.Errorf("numeric trace filter field %q requires an integer value: %w", clause.Field, err)
		}
		return fmt.Sprintf("toInt64OrZero(%s) %s ?", expr, sqlOp), []any{value}, nil
	case clauseFieldStatus:
		predicate, err := statusCodePredicate(expr, clause.Op, clause.Value)
		if err != nil {
			return "", nil, err
		}
		return predicate, nil, nil
	default:
		return fmt.Sprintf("%s %s ?", expr, sqlOp), []any{clause.Value}, nil
	}
}

func compileMetadataClausePredicate(clause TraceFilterClause) (string, []any, error) {
	key := strings.TrimPrefix(clause.Field, metadataFieldPrefix)
	if !safeAttrKey.MatchString(key) {
		return "", nil, fmt.Errorf("unsafe metadata trace filter key %q", key)
	}

	if clause.Op == OpExists {
		return "SpanAttributes[?] != ''", []any{key}, nil
	}

	sqlOp, err := clauseSQLOp(clause.Op)
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("SpanAttributes[?] %s ?", sqlOp), []any{key, clause.Value}, nil
}

func traceClauseFieldExpr(field string) (string, clauseFieldKind, error) {
	switch field {
	case "trace":
		return "TraceId", clauseFieldString, nil
	case "agent":
		return agentNameSQL(), clauseFieldString, nil
	case "model":
		return modelSQL(), clauseFieldString, nil
	case "provider":
		return providerSQL(), clauseFieldString, nil
	case "user":
		return userSQL(), clauseFieldString, nil
	case "session":
		return sessionSQL(), clauseFieldString, nil
	case "thread":
		return threadSQL(), clauseFieldString, nil
	case "correlation":
		return correlationSQL(), clauseFieldString, nil
	case "status":
		return "StatusCode", clauseFieldStatus, nil
	case "tool.name":
		return toolNameSQL(), clauseFieldString, nil
	case "tokens", "tokens.total":
		return totalTokensSQL(), clauseFieldNumber, nil
	case "ttft":
		return ttftSQL(), clauseFieldNumber, nil
	default:
		return "", 0, fmt.Errorf("unknown trace filter field %q", field)
	}
}

func clauseSQLOp(op ClauseOp) (string, error) {
	switch op {
	case OpEq, OpNe, OpGt, OpGte, OpLt, OpLte:
		return string(op), nil
	default:
		return "", fmt.Errorf("unknown trace filter operator %q", op)
	}
}

// statusCodePredicate renders a status filter as a membership test instead of
// a literal comparison.
//
// otel_traces stores a span's status under two spellings depending on the
// ingest path (see internal/telemetry/otelstatus), and gateway-produced spans
// only ever carry the short one. Binding the enum name as a parameter to
// `StatusCode = ?` therefore matched nothing, so `status = ERROR` silently
// returned an empty result set instead of the failing traces.
//
// Only equality is meaningful for a status, and the two negations are not
// symmetric: `status != ERROR` must keep spans with no status at all, whereas
// `status != OK` must drop them. IsNotError and IsNotOK encode that split.
func statusCodePredicate(expr string, op ClauseOp, value string) (string, error) {
	var wantError bool
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "OK", "STATUS_CODE_OK":
		wantError = false
	case "ERROR", "STATUS_CODE_ERROR":
		wantError = true
	default:
		return "", fmt.Errorf("status trace filter value %q must be OK or ERROR", value)
	}

	switch op {
	case OpEq:
		if wantError {
			return otelstatus.IsError(expr), nil
		}
		return otelstatus.IsOK(expr), nil
	case OpNe:
		if wantError {
			return otelstatus.IsNotError(expr), nil
		}
		return otelstatus.IsNotOK(expr), nil
	default:
		return "", fmt.Errorf("status trace filter supports only = and !=, got %q", op)
	}
}
