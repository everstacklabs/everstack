package autoscorer

import (
	"context"
	"regexp"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// PolicyConfig defines the rules for compliance scoring.
// Loaded from agent config JSONB under "autoscorer.policy".
type PolicyConfig struct {
	// BlockedPatterns are regex patterns that should never appear in output.
	// Examples: SSN patterns, API key patterns, internal URLs.
	BlockedPatterns []string `json:"blocked_patterns,omitempty"`

	// BlockedKeywords are exact substrings that should never appear (case-insensitive).
	BlockedKeywords []string `json:"blocked_keywords,omitempty"`

	// MaxOutputLength is the maximum allowed assistant response length in characters.
	// 0 = unlimited.
	MaxOutputLength int `json:"max_output_length,omitempty"`
}

// PolicyComplianceScorer checks agent output against configurable policy rules.
//
// Scores produced:
//   - policy.compliant:          boolean — no policy violations detected
//   - policy.blocked_pattern:    boolean — a blocked regex pattern was found in output
//   - policy.output_length:      boolean — output exceeds configured max length
type PolicyComplianceScorer struct {
	config  PolicyConfig
	compiled []*regexp.Regexp
}

// NewPolicyComplianceScorer creates a scorer with the given policy config.
// Invalid regex patterns are silently skipped.
func NewPolicyComplianceScorer(cfg PolicyConfig) *PolicyComplianceScorer {
	s := &PolicyComplianceScorer{config: cfg}
	for _, pattern := range cfg.BlockedPatterns {
		if re, err := regexp.Compile(pattern); err == nil {
			s.compiled = append(s.compiled, re)
		}
	}
	return s
}

func (s *PolicyComplianceScorer) Name() string { return "policy" }

func (s *PolicyComplianceScorer) Score(_ context.Context, tc *TurnContext) []*scores.Score {
	if tc.AssistantText == "" {
		return nil
	}

	var out []*scores.Score
	compliant := true

	// 1. Blocked patterns (regex)
	patternViolation := false
	var matchedPattern string
	for _, re := range s.compiled {
		if re.MatchString(tc.AssistantText) {
			patternViolation = true
			matchedPattern = re.String()
			compliant = false
			break
		}
	}
	s1 := scores.BooleanScore(tc.TraceID, "policy.blocked_pattern", patternViolation, scores.ScoreSourceEval)
	s1.Metadata = map[string]interface{}{
		"turn_number": tc.TurnNumber,
	}
	if matchedPattern != "" {
		s1.Metadata["matched_pattern"] = matchedPattern
	}
	out = append(out, s1)

	// 2. Blocked keywords (case-insensitive exact match)
	lower := strings.ToLower(tc.AssistantText)
	for _, kw := range s.config.BlockedKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			compliant = false
			break
		}
	}

	// 3. Output length
	if s.config.MaxOutputLength > 0 {
		tooLong := len(tc.AssistantText) > s.config.MaxOutputLength
		if tooLong {
			compliant = false
		}
		s3 := scores.BooleanScore(tc.TraceID, "policy.output_length", tooLong, scores.ScoreSourceEval)
		s3.Metadata = map[string]interface{}{
			"turn_number":    tc.TurnNumber,
			"output_length":  len(tc.AssistantText),
			"max_length":     s.config.MaxOutputLength,
		}
		out = append(out, s3)
	}

	// 4. Overall compliance (aggregate)
	s4 := scores.BooleanScore(tc.TraceID, "policy.compliant", compliant, scores.ScoreSourceEval)
	s4.Metadata = map[string]interface{}{
		"turn_number": tc.TurnNumber,
	}
	out = append(out, s4)

	return out
}

// BuiltInPolicyPatterns returns common PII/secret patterns that can be used
// as defaults when no custom policy is configured.
func BuiltInPolicyPatterns() []string {
	return []string{
		// US Social Security Number
		`\b\d{3}-\d{2}-\d{4}\b`,
		// Credit card (basic Luhn-eligible patterns)
		`\b(?:4[0-9]{12}(?:[0-9]{3})?|5[1-5][0-9]{14}|3[47][0-9]{13})\b`,
		// AWS access key
		`\bAKIA[0-9A-Z]{16}\b`,
		// Generic API key pattern (long hex/base64 strings after "key" or "token")
		`(?i)(?:api[_-]?key|secret|token)\s*[:=]\s*["']?[A-Za-z0-9+/=_-]{32,}`,
	}
}
