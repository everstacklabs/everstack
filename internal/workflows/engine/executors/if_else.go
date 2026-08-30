package executors

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// IfElseExecutor handles conditional branching (true/false paths).
type IfElseExecutor struct{}

func (e *IfElseExecutor) NodeType() string { return "ifElse" }

func (e *IfElseExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	conditionType := node.GetConfigString("conditionType")
	if conditionType == "" {
		conditionType = "expression"
	}

	expression := node.GetConfigString("conditionExpression")

	var result bool

	switch conditionType {
	case "expression":
		result = evaluateExpression(expression, ec)
	case "hasResponse":
		result = ec.Response != nil
	case "cacheHit":
		result = ec.CacheHit
	case "authenticated":
		result = ec.Authenticated
	case "variableEquals":
		// Format: "variableName == value"
		result = evaluateExpression(expression, ec)
	default:
		logger.WithFields("conditionType", conditionType).Warn("ifElse executor: unknown condition type")
		result = false
	}

	ec.ConditionResult = result
	ec.SetNodeData("condition_type", conditionType)
	ec.SetNodeData("expression", expression)
	ec.SetNodeData("result", fmt.Sprintf("%v", result))

	output := map[string]interface{}{
		"condition_type": conditionType,
		"expression":     expression,
		"result":         result,
	}

	if result {
		return engine.NodeResult{NextHandle: "true", Output: output}
	}
	return engine.NodeResult{NextHandle: "false", Output: output}
}

// evaluateExpression evaluates a simple expression against the execution context.
// Supports: "variable == value", "variable != value", "variable contains value",
// "variable exists", "response.status == 200"
func evaluateExpression(expr string, ec *engine.ExecutionContext) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}

	// Check for operators
	operators := []struct {
		op   string
		eval func(left, right string) bool
	}{
		{"!=", func(l, r string) bool { return l != r }},
		{"==", func(l, r string) bool { return l == r }},
		{"contains", func(l, r string) bool { return strings.Contains(l, r) }},
	}

	for _, op := range operators {
		parts := strings.SplitN(expr, op.op, 2)
		if len(parts) == 2 {
			left := resolveValue(strings.TrimSpace(parts[0]), ec)
			right := strings.TrimSpace(parts[1])
			// Strip quotes from right side
			right = strings.Trim(right, `"'`)
			return op.eval(left, right)
		}
	}

	// Check for "exists" operator
	if strings.HasSuffix(expr, " exists") {
		varName := strings.TrimSuffix(expr, " exists")
		varName = strings.TrimSpace(varName)
		_, ok := ec.GetVariable(varName)
		return ok
	}

	// Treat as a variable name -- truthy if non-empty
	val := resolveValue(expr, ec)
	return val != "" && val != "false" && val != "0"
}

// resolveValue resolves a reference to a value from the execution context.
func resolveValue(ref string, ec *engine.ExecutionContext) string {
	ref = strings.TrimSpace(ref)

	// Try ledger expression resolver for $ prefixed references
	if strings.HasPrefix(ref, "$") && ec.Ledger != nil {
		resolved := ec.Ledger.Resolve(ref, ec)
		if resolved != ref {
			return resolved
		}
	}

	// Check execution context variables
	if v, ok := ec.GetVariable(ref); ok {
		return fmt.Sprintf("%v", v)
	}

	// Check metadata
	if strings.HasPrefix(ref, "meta.") {
		key := strings.TrimPrefix(ref, "meta.")
		if v, ok := ec.Metadata[key]; ok {
			return v
		}
	}

	// Check special variables
	switch ref {
	case "response.content":
		return ec.LastAssistantContent()
	case "authenticated":
		if ec.Authenticated {
			return "true"
		}
		return "false"
	case "cacheHit":
		if ec.CacheHit {
			return "true"
		}
		return "false"
	}

	return ref // Return as literal
}
