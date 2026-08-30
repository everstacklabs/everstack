package executors

import (
	"context"
	"fmt"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// InputGuardrailsExecutor handles input validation and content filtering.
//
// Config fields (from frontend InputGuardrailsConfig):
//   - piiDetection: bool — check for PII in input (default: false)
//   - promptInjection: bool — check for prompt injection attempts (default: false)
//   - contentFilter: bool — use moderation API for content policy violations (default: false)
//
// When contentFilter is enabled and a ModerationProvider is available in the registry,
// the executor calls the moderation API to check the user's input. If any category is
// flagged, the input is blocked.
//
// When piiDetection or promptInjection is enabled, keyword-based heuristics are applied.
//
// Handles: "pass" if input is clean, "block" if input is flagged.
type InputGuardrailsExecutor struct {
	Registry *gw.Registry
}

func (e *InputGuardrailsExecutor) NodeType() string { return "inputGuardrails" }

func (e *InputGuardrailsExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	piiDetection := node.GetConfigBool("piiDetection")
	promptInjection := node.GetConfigBool("promptInjection")
	contentFilter := node.GetConfigBool("contentFilter")

	// If no checks are enabled, pass through
	if !piiDetection && !promptInjection && !contentFilter {
		ec.InputBlocked = false
		return engine.NodeResult{NextHandle: "pass"}
	}

	// Extract user input text
	userInput := extractUserQuery(ec)
	if userInput == "" {
		ec.InputBlocked = false
		return engine.NodeResult{NextHandle: "pass"}
	}

	var violations []string

	// 1. Prompt injection detection (heuristic keyword-based)
	if promptInjection {
		if reason := detectPromptInjection(userInput); reason != "" {
			violations = append(violations, "prompt_injection: "+reason)
		}
	}

	// 2. PII detection (heuristic pattern-based)
	if piiDetection {
		if reason := detectPII(userInput); reason != "" {
			violations = append(violations, "pii: "+reason)
		}
	}

	// 3. Content filter via moderation API
	if contentFilter {
		if reason := e.moderateContent(ctx, userInput); reason != "" {
			violations = append(violations, "content_filter: "+reason)
		}
	}

	// Emit a guardrail span event so the trace shows the safety check (D7).
	telemetry.RecordGuardrailCheck(ctx, "guardrail.input", violations)

	if len(violations) > 0 {
		ec.InputBlocked = true
		ec.SetVariable("input_guardrail_violations", strings.Join(violations, "; "))
		ec.SetNodeData("result", "block")
		ec.SetNodeData("violations", strings.Join(violations, "; "))
		logger.WithFields("violations", strings.Join(violations, "; ")).
			Warn("input guardrails: input blocked")
		return engine.NodeResult{NextHandle: "block", Output: map[string]interface{}{
			"passed":     false,
			"blocked":    true,
			"violations": violations,
		}}
	}

	ec.InputBlocked = false
	ec.SetNodeData("result", "pass")
	logger.Debug("input guardrails: input passed all checks")
	return engine.NodeResult{NextHandle: "pass", Output: map[string]interface{}{
		"passed":  true,
		"blocked": false,
	}}
}

// moderateContent uses the ModerationProvider to check content for policy violations.
func (e *InputGuardrailsExecutor) moderateContent(ctx context.Context, text string) string {
	if e.Registry == nil {
		return ""
	}

	mp, _, found := e.Registry.FindModerationProvider()
	if !found {
		logger.Debug("input guardrails: no moderation provider available, skipping content filter")
		return ""
	}

	resp, err := mp.Moderate(ctx, gw.ModerationRequest{Input: text})
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("input guardrails: moderation API call failed")
		return ""
	}

	for _, result := range resp.Results {
		if result.Flagged {
			return formatModerationViolations(result)
		}
	}
	return ""
}

// formatModerationViolations returns a summary of flagged categories.
func formatModerationViolations(result gw.ModerationResult) string {
	var flagged []string
	if result.Categories.Hate {
		flagged = append(flagged, "hate")
	}
	if result.Categories.HateThreatening {
		flagged = append(flagged, "hate/threatening")
	}
	if result.Categories.Harassment {
		flagged = append(flagged, "harassment")
	}
	if result.Categories.HarassmentThreatening {
		flagged = append(flagged, "harassment/threatening")
	}
	if result.Categories.SelfHarm {
		flagged = append(flagged, "self-harm")
	}
	if result.Categories.SelfHarmIntent {
		flagged = append(flagged, "self-harm/intent")
	}
	if result.Categories.Sexual {
		flagged = append(flagged, "sexual")
	}
	if result.Categories.SexualMinors {
		flagged = append(flagged, "sexual/minors")
	}
	if result.Categories.Violence {
		flagged = append(flagged, "violence")
	}
	if result.Categories.ViolenceGraphic {
		flagged = append(flagged, "violence/graphic")
	}
	if result.Categories.Illicit {
		flagged = append(flagged, "illicit")
	}
	if result.Categories.IllicitViolent {
		flagged = append(flagged, "illicit/violent")
	}
	if result.Categories.SelfHarmInstructions {
		flagged = append(flagged, "self-harm/instructions")
	}
	if len(flagged) == 0 {
		return "content flagged"
	}
	return fmt.Sprintf("flagged categories: %s", strings.Join(flagged, ", "))
}

// detectPromptInjection checks for common prompt injection patterns.
func detectPromptInjection(text string) string {
	lower := strings.ToLower(text)

	patterns := []struct {
		pattern string
		reason  string
	}{
		{"ignore previous instructions", "instruction override attempt"},
		{"ignore all previous", "instruction override attempt"},
		{"disregard previous", "instruction override attempt"},
		{"forget your instructions", "instruction override attempt"},
		{"you are now", "role hijacking attempt"},
		{"act as if you", "role hijacking attempt"},
		{"pretend you are", "role hijacking attempt"},
		{"new instructions:", "instruction injection"},
		{"system prompt:", "system prompt extraction"},
		{"reveal your system", "system prompt extraction"},
		{"what are your instructions", "system prompt extraction"},
		{"repeat your system prompt", "system prompt extraction"},
	}

	for _, p := range patterns {
		if strings.Contains(lower, p.pattern) {
			return p.reason
		}
	}
	return ""
}

// detectPII checks for common PII patterns using simple heuristics.
func detectPII(text string) string {
	var detectedTypes []string

	// SSN pattern: XXX-XX-XXXX
	if containsSSNPattern(text) {
		detectedTypes = append(detectedTypes, "SSN")
	}

	// Credit card: 13-19 digit sequences
	if containsCreditCardPattern(text) {
		detectedTypes = append(detectedTypes, "credit card")
	}

	// Email pattern
	if containsEmailPattern(text) {
		detectedTypes = append(detectedTypes, "email")
	}

	if len(detectedTypes) > 0 {
		return fmt.Sprintf("detected: %s", strings.Join(detectedTypes, ", "))
	}
	return ""
}

// containsSSNPattern checks for SSN-like patterns (XXX-XX-XXXX).
func containsSSNPattern(text string) bool {
	for i := 0; i < len(text)-10; i++ {
		if isDigit(text[i]) && isDigit(text[i+1]) && isDigit(text[i+2]) &&
			text[i+3] == '-' &&
			isDigit(text[i+4]) && isDigit(text[i+5]) &&
			text[i+6] == '-' &&
			isDigit(text[i+7]) && isDigit(text[i+8]) && isDigit(text[i+9]) && isDigit(text[i+10]) {
			return true
		}
	}
	return false
}

// containsCreditCardPattern checks for sequences of 13+ consecutive digits.
func containsCreditCardPattern(text string) bool {
	consecutiveDigits := 0
	for _, c := range text {
		if c >= '0' && c <= '9' {
			consecutiveDigits++
			if consecutiveDigits >= 13 {
				return true
			}
		} else if c == ' ' || c == '-' {
			// Allow separators within card numbers
			continue
		} else {
			consecutiveDigits = 0
		}
	}
	return false
}

// containsEmailPattern checks for email-like patterns.
func containsEmailPattern(text string) bool {
	atIdx := strings.Index(text, "@")
	if atIdx <= 0 || atIdx >= len(text)-3 {
		return false
	}
	// Check for a dot after the @
	afterAt := text[atIdx+1:]
	dotIdx := strings.Index(afterAt, ".")
	return dotIdx > 0 && dotIdx < len(afterAt)-1
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
