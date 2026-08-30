package executors

import (
	"context"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// OutputGuardrailsExecutor handles output validation and content filtering.
//
// Config fields (from frontend OutputGuardrailsConfig):
//   - jailbreakDetection: bool — check for jailbreak indicators in output (default: false)
//   - hallucinationDetection: bool — reserved for future hallucination checks (default: false)
//   - toxicityDetection: bool — use moderation API for toxicity/policy violations (default: false)
//
// When toxicityDetection is enabled and a ModerationProvider is available, the executor
// calls the moderation API to check the assistant's output. If any category is flagged,
// the output is blocked.
//
// Handles: "pass" if output is clean, "block" if output is flagged.
type OutputGuardrailsExecutor struct {
	Registry *gw.Registry
}

func (e *OutputGuardrailsExecutor) NodeType() string { return "outputGuardrails" }

func (e *OutputGuardrailsExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	jailbreakDetection := node.GetConfigBool("jailbreakDetection")
	toxicityDetection := node.GetConfigBool("toxicityDetection")
	// hallucinationDetection is reserved for future use
	// hallucinationDetection := node.GetConfigBool("hallucinationDetection")

	// If no checks are enabled, pass through
	if !jailbreakDetection && !toxicityDetection {
		ec.OutputBlocked = false
		return engine.NodeResult{NextHandle: "pass"}
	}

	// Extract assistant output text
	outputText := ec.LastAssistantContent()
	if outputText == "" {
		ec.OutputBlocked = false
		return engine.NodeResult{NextHandle: "pass"}
	}

	var violations []string

	// 1. Jailbreak detection — checks for indicators that the model has been jailbroken
	if jailbreakDetection {
		if reason := detectJailbreakOutput(outputText); reason != "" {
			violations = append(violations, "jailbreak: "+reason)
		}
	}

	// 2. Toxicity detection via moderation API
	if toxicityDetection {
		if reason := e.moderateOutput(ctx, outputText); reason != "" {
			violations = append(violations, "toxicity: "+reason)
		}
	}

	// Emit a guardrail span event so the trace shows the safety check (D7).
	telemetry.RecordGuardrailCheck(ctx, "guardrail.output", violations)

	if len(violations) > 0 {
		ec.OutputBlocked = true
		ec.SetVariable("output_guardrail_violations", strings.Join(violations, "; "))
		ec.SetNodeData("result", "block")
		ec.SetNodeData("violations", strings.Join(violations, "; "))
		logger.WithFields("violations", strings.Join(violations, "; ")).
			Warn("output guardrails: output blocked")
		return engine.NodeResult{NextHandle: "block", Output: map[string]interface{}{
			"passed":     false,
			"blocked":    true,
			"violations": violations,
		}}
	}

	ec.OutputBlocked = false
	ec.SetNodeData("result", "pass")
	logger.Debug("output guardrails: output passed all checks")
	return engine.NodeResult{NextHandle: "pass", Output: map[string]interface{}{
		"passed":  true,
		"blocked": false,
	}}
}

// moderateOutput uses the ModerationProvider to check output for policy violations.
func (e *OutputGuardrailsExecutor) moderateOutput(ctx context.Context, text string) string {
	if e.Registry == nil {
		return ""
	}

	mp, _, found := e.Registry.FindModerationProvider()
	if !found {
		logger.Debug("output guardrails: no moderation provider available, skipping toxicity check")
		return ""
	}

	resp, err := mp.Moderate(ctx, gw.ModerationRequest{Input: text})
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("output guardrails: moderation API call failed")
		return ""
	}

	for _, result := range resp.Results {
		if result.Flagged {
			return formatModerationViolations(result)
		}
	}
	return ""
}

// detectJailbreakOutput checks for indicators that the model's output may be jailbroken.
func detectJailbreakOutput(text string) string {
	lower := strings.ToLower(text)

	patterns := []struct {
		pattern string
		reason  string
	}{
		{"as an ai language model, i cannot", "refusal bypass detected"},
		{"[jailbreak]", "explicit jailbreak marker"},
		{"[developer mode]", "developer mode marker"},
		{"[dan mode]", "DAN mode marker"},
		{"sure, here's how to hack", "harmful instruction generation"},
		{"here's how to create a", "potentially harmful generation"},
		{"i'm now in developer mode", "mode switching detected"},
		{"my previous instructions have been", "instruction override indicator"},
	}

	for _, p := range patterns {
		if strings.Contains(lower, p.pattern) {
			return p.reason
		}
	}
	return ""
}
