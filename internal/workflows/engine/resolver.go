package engine

import (
	"fmt"
	"strings"
)

// Resolve resolves a ledger expression to a string value.
//
// Supported expressions:
//
//	$prev.{field}          — Previous node's output field (e.g., $prev.content)
//	$prev.data.{field}     — Alias for $prev.{field}
//	$node.{nodeID}.{field} — Specific node's output by ID
//	$label.{Label}.{field} — Specific node's output by label
//	$var.{name}            — Legacy variable from ec.Variables
//	$input                 — Original user input text
//	$meta.{key}            — Execution metadata value
func (l *ExecutionLedger) Resolve(expr string, ec *ExecutionContext) string {
	if l == nil || expr == "" {
		return expr
	}

	expr = strings.TrimSpace(expr)

	switch {
	case expr == "$input":
		return ec.OriginalUserInput()

	case strings.HasPrefix(expr, "$prev.data."):
		field := expr[len("$prev.data."):]
		if prev := l.Previous(); prev != nil {
			return prev.GetString(field)
		}
		return ""

	case strings.HasPrefix(expr, "$prev."):
		field := expr[len("$prev."):]
		if prev := l.Previous(); prev != nil {
			return prev.GetString(field)
		}
		return ""

	case strings.HasPrefix(expr, "$node."):
		// $node.{nodeID}.{field}
		rest := expr[len("$node."):]
		dotIdx := strings.Index(rest, ".")
		if dotIdx < 0 {
			return ""
		}
		nodeID := rest[:dotIdx]
		field := rest[dotIdx+1:]
		if output := l.Get(nodeID); output != nil {
			return output.GetString(field)
		}
		return ""

	case strings.HasPrefix(expr, "$label."):
		// $label.{Label}.{field}
		rest := expr[len("$label."):]
		dotIdx := strings.Index(rest, ".")
		if dotIdx < 0 {
			return ""
		}
		label := rest[:dotIdx]
		field := rest[dotIdx+1:]
		if output := l.GetByLabel(label); output != nil {
			return output.GetString(field)
		}
		return ""

	case strings.HasPrefix(expr, "$var."):
		varName := expr[len("$var."):]
		if v, ok := ec.GetVariable(varName); ok {
			return fmt.Sprintf("%v", v)
		}
		return ""

	case strings.HasPrefix(expr, "$meta."):
		key := expr[len("$meta."):]
		if v, ok := ec.Metadata[key]; ok {
			return v
		}
		return ""
	}

	return expr
}

// InterpolateTemplate replaces all {{expression}} patterns in a template string.
// It first tries ledger expressions (those starting with $), then falls back to
// legacy variable interpolation for backward compatibility.
func (l *ExecutionLedger) InterpolateTemplate(template string, ec *ExecutionContext) string {
	if l == nil || !strings.Contains(template, "{{") {
		return template
	}

	result := template
	for {
		start := strings.Index(result, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}}")
		if end < 0 {
			break
		}
		end += start

		expr := strings.TrimSpace(result[start+2 : end])
		var replacement string
		if strings.HasPrefix(expr, "$") {
			replacement = l.Resolve(expr, ec)
		} else {
			// Legacy: try ec.Variables, then ec.Metadata
			if v, ok := ec.GetVariable(expr); ok {
				replacement = fmt.Sprintf("%v", v)
			} else if strings.HasPrefix(expr, "meta.") {
				key := strings.TrimPrefix(expr, "meta.")
				replacement = ec.Metadata[key]
			} else {
				replacement = result[start : end+2] // Keep original if unresolved
			}
		}

		result = result[:start] + replacement + result[end+2:]
	}

	return result
}
