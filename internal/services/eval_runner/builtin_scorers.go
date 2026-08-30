package eval_runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Built-in deterministic scorers. Selected by giving a score config a
// data_type of "builtin_<name>". The `eval_prompt` column holds a JSON
// blob of parameters (specific to each scorer; empty for parameter-less
// ones). The runner dispatches via runBuiltinScorer before falling
// through to the LLM-judge / code-scorer paths.
//
// These are the deterministic side of the Braintrust scorer library:
// no LLM calls, no sandbox — just Go. The LLM-judge side (factuality,
// faithfulness, etc.) ships as pre-canned prompt templates in
// builtin_prompts.go.

// IsBuiltinScorer reports whether a config should be dispatched to a
// built-in scorer.
func IsBuiltinScorer(dataType string) bool {
	return strings.HasPrefix(strings.ToLower(dataType), "builtin_")
}

// BuiltinScorerInput is the surface every built-in scorer sees.
type BuiltinScorerInput struct {
	Input          interface{}
	Output         interface{}
	ExpectedOutput interface{}
	Metadata       interface{}
	Context        string
	// Params decoded from cfg.EvalPrompt. Always present (may be empty).
	Params map[string]interface{}
}

// builtinScorerFn returns either a 0..1 numeric score or a boolean.
// Reasons are short, human-readable.
type builtinScorerFn func(in BuiltinScorerInput) (value interface{}, reason string, err error)

var builtinScorers = map[string]builtinScorerFn{
	"builtin_exact_match":              scoreExactMatch,
	"builtin_contains":                 scoreContains,
	"builtin_regex_match":              scoreRegexMatch,
	"builtin_json_validity":            scoreJSONValidity,
	"builtin_schema_conformance":       scoreSchemaConformance,
	"builtin_levenshtein":              scoreLevenshtein,
	"builtin_length_budget":            scoreLengthBudget,
	"builtin_no_refusal":               scoreNoRefusal,
	"builtin_format_adherence":         scoreFormatAdherence,
	"builtin_pii_leak_regex":           scorePIILeakRegex,
	"builtin_citation_presence":        scoreCitationPresence,
	"builtin_language_match":           scoreLanguageMatch,
	"builtin_tool_correctness":         scoreToolCorrectness,
	"builtin_tool_correctness_ordered": scoreToolCorrectnessOrdered,
}

// BuiltinScorerNames returns the registered scorer names. Stable order.
func BuiltinScorerNames() []string {
	names := make([]string, 0, len(builtinScorers))
	for name := range builtinScorers {
		names = append(names, name)
	}
	// Stable order so the UI / tests don't flake.
	regSorted := names
	for i := 0; i < len(regSorted); i++ {
		for j := i + 1; j < len(regSorted); j++ {
			if regSorted[j] < regSorted[i] {
				regSorted[i], regSorted[j] = regSorted[j], regSorted[i]
			}
		}
	}
	return regSorted
}

// runBuiltinScorer dispatches a score config to its built-in implementation.
// Returns (nil, false, nil) if the config doesn't match any registered
// scorer — caller falls through to the next dispatch arm.
func runBuiltinScorer(_ context.Context, cfg ScoreConfig, input, output, expectedOutput, metadata interface{}, retrievedContext string) (*ScoreResult, bool, error) {
	kind := strings.ToLower(cfg.DataType)
	fn, ok := builtinScorers[kind]
	if !ok {
		return nil, false, nil
	}

	var params map[string]interface{}
	if cfg.EvalPrompt != "" {
		if err := json.Unmarshal([]byte(cfg.EvalPrompt), &params); err != nil {
			// Treat unparseable params as empty rather than failing.
			params = map[string]interface{}{}
		}
	}
	if params == nil {
		params = map[string]interface{}{}
	}

	value, reason, err := fn(BuiltinScorerInput{
		Input:          input,
		Output:         output,
		ExpectedOutput: expectedOutput,
		Metadata:       metadata,
		Context:        retrievedContext,
		Params:         params,
	})
	if err != nil {
		return nil, true, err
	}
	return &ScoreResult{Name: cfg.Name, Value: value, Reason: reason}, true, nil
}

// ─── Scorer implementations ────────────────────────────────────────────────

func scoreExactMatch(in BuiltinScorerInput) (interface{}, string, error) {
	got := toStringForScore(in.Output)
	want := toStringForScore(in.ExpectedOutput)
	if got == "" && want == "" {
		return false, "both empty", nil
	}
	return got == want, fmt.Sprintf("len(got)=%d len(want)=%d", len(got), len(want)), nil
}

func scoreContains(in BuiltinScorerInput) (interface{}, string, error) {
	needle, _ := in.Params["needle"].(string)
	if needle == "" {
		return false, "missing 'needle' param", nil
	}
	haystack := toStringForScore(in.Output)
	caseInsensitive, _ := in.Params["case_insensitive"].(bool)
	if caseInsensitive {
		return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle)),
			"case-insensitive contains check", nil
	}
	return strings.Contains(haystack, needle), "contains check", nil
}

func scoreRegexMatch(in BuiltinScorerInput) (interface{}, string, error) {
	pattern, _ := in.Params["pattern"].(string)
	if pattern == "" {
		return false, "missing 'pattern' param", nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, "", fmt.Errorf("invalid regex %q: %w", pattern, err)
	}
	return re.MatchString(toStringForScore(in.Output)), "regex match", nil
}

func scoreJSONValidity(in BuiltinScorerInput) (interface{}, string, error) {
	s := toStringForScore(in.Output)
	if s == "" {
		return false, "empty output", nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return false, "invalid JSON: " + err.Error(), nil
	}
	return true, "valid JSON", nil
}

// scoreSchemaConformance accepts a JSON Schema in `params["schema"]` and
// performs lightweight checks (top-level type + required fields). Full
// schema validation would pull in a JSON-schema lib; this covers the
// common "shape" cases without a dep.
func scoreSchemaConformance(in BuiltinScorerInput) (interface{}, string, error) {
	schemaAny, ok := in.Params["schema"]
	if !ok {
		return false, "missing 'schema' param", nil
	}
	schema, ok := schemaAny.(map[string]interface{})
	if !ok {
		return false, "schema must be an object", nil
	}
	var output interface{}
	if err := json.Unmarshal([]byte(toStringForScore(in.Output)), &output); err != nil {
		return false, "output is not JSON", nil
	}

	expectedType, _ := schema["type"].(string)
	if expectedType != "" {
		switch expectedType {
		case "object":
			if _, ok := output.(map[string]interface{}); !ok {
				return false, "expected JSON object", nil
			}
		case "array":
			if _, ok := output.([]interface{}); !ok {
				return false, "expected JSON array", nil
			}
		case "string":
			if _, ok := output.(string); !ok {
				return false, "expected string", nil
			}
		case "number", "integer":
			if _, ok := output.(float64); !ok {
				return false, "expected number", nil
			}
		}
	}

	if required, ok := schema["required"].([]interface{}); ok {
		obj, ok := output.(map[string]interface{})
		if !ok {
			return false, "required[] needs an object output", nil
		}
		missing := []string{}
		for _, r := range required {
			if k, ok := r.(string); ok {
				if _, present := obj[k]; !present {
					missing = append(missing, k)
				}
			}
		}
		if len(missing) > 0 {
			return false, "missing required fields: " + strings.Join(missing, ", "), nil
		}
	}
	return true, "conforms to schema", nil
}

// scoreLevenshtein returns 1 - normalised_edit_distance, in [0, 1].
// Higher = closer to expected.
func scoreLevenshtein(in BuiltinScorerInput) (interface{}, string, error) {
	a := toStringForScore(in.Output)
	b := toStringForScore(in.ExpectedOutput)
	if a == "" && b == "" {
		return 1.0, "both empty", nil
	}
	if a == "" || b == "" {
		return 0.0, "one side empty", nil
	}
	d := levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	sim := 1 - float64(d)/float64(maxLen)
	if sim < 0 {
		sim = 0
	}
	return sim, fmt.Sprintf("distance=%d max=%d", d, maxLen), nil
}

func scoreLengthBudget(in BuiltinScorerInput) (interface{}, string, error) {
	budget, ok := paramFloat(in.Params, "max_chars")
	if !ok {
		return false, "missing 'max_chars' param", nil
	}
	length := len(toStringForScore(in.Output))
	return float64(length) <= budget, fmt.Sprintf("len=%d budget=%.0f", length, budget), nil
}

func scoreNoRefusal(in BuiltinScorerInput) (interface{}, string, error) {
	out := strings.ToLower(toStringForScore(in.Output))
	refusalPatterns := []string{
		"i cannot",
		"i can't",
		"i'm sorry, but",
		"i am sorry, but",
		"i am unable to",
		"i'm unable to",
		"as an ai language model",
		"i don't have the ability",
		"i'm not able to",
		"sorry, but i can't",
	}
	for _, p := range refusalPatterns {
		if strings.Contains(out, p) {
			return false, "matched refusal phrase: " + p, nil
		}
	}
	return true, "no refusal phrases detected", nil
}

func scoreFormatAdherence(in BuiltinScorerInput) (interface{}, string, error) {
	out := toStringForScore(in.Output)
	prefix, _ := in.Params["prefix"].(string)
	suffix, _ := in.Params["suffix"].(string)
	if prefix != "" && !strings.HasPrefix(out, prefix) {
		return false, "missing prefix", nil
	}
	if suffix != "" && !strings.HasSuffix(out, suffix) {
		return false, "missing suffix", nil
	}
	return true, "format ok", nil
}

// scorePIILeakRegex detects common PII patterns in the output. Matches =
// score 0 (PII detected). No matches = score 1 (clean).
var piiPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"email", regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)},
	{"phone_us", regexp.MustCompile(`\b\(?\d{3}\)?[\s.\-]?\d{3}[\s.\-]?\d{4}\b`)},
	{"ssn", regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)},
	{"ipv4", regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)},
	{"credit_card", regexp.MustCompile(`\b(?:\d{4}[\s\-]?){3}\d{4}\b`)},
}

func scorePIILeakRegex(in BuiltinScorerInput) (interface{}, string, error) {
	out := toStringForScore(in.Output)
	matched := []string{}
	for _, p := range piiPatterns {
		if p.pattern.MatchString(out) {
			matched = append(matched, p.name)
		}
	}
	if len(matched) > 0 {
		return false, "PII detected: " + strings.Join(matched, ", "), nil
	}
	return true, "no PII patterns matched", nil
}

func scoreCitationPresence(in BuiltinScorerInput) (interface{}, string, error) {
	out := toStringForScore(in.Output)
	// Common citation shapes: [1], [^1], (Source: ...), Source:, doi:..., URL.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\[\d+\]`),
		regexp.MustCompile(`\[\^?\d+\]`),
		regexp.MustCompile(`(?i)\bsource:\s*\S`),
		regexp.MustCompile(`(?i)\bcitation:\s*\S`),
		regexp.MustCompile(`\b(?:https?|doi)://?\S+`),
	}
	for _, p := range patterns {
		if p.MatchString(out) {
			return true, "citation pattern matched: " + p.String(), nil
		}
	}
	return false, "no citation pattern matched", nil
}

func scoreLanguageMatch(in BuiltinScorerInput) (interface{}, string, error) {
	// Light heuristic: caller passes expected ascii alphabet ratio or a
	// regex of allowed chars in `params["allowed_chars"]`.
	allowed, _ := in.Params["allowed_chars"].(string)
	if allowed == "" {
		// Default: ASCII letters + common punctuation.
		allowed = `[A-Za-z0-9\s\.,;:'!\?\-\(\)"]`
	}
	re, err := regexp.Compile("^" + allowed + "+$")
	if err != nil {
		return false, "", fmt.Errorf("invalid allowed_chars regex: %w", err)
	}
	return re.MatchString(toStringForScore(in.Output)), "language-match heuristic", nil
}

func scoreToolCorrectness(in BuiltinScorerInput) (interface{}, string, error) {
	ordered, _ := paramBool(in.Params, "ordered")
	if !ordered {
		ordered, _ = paramBool(in.Params, "require_ordered")
	}
	return scoreToolCorrectnessInternal(in, ordered)
}

func scoreToolCorrectnessOrdered(in BuiltinScorerInput) (interface{}, string, error) {
	return scoreToolCorrectnessInternal(in, true)
}

func scoreToolCorrectnessInternal(in BuiltinScorerInput, ordered bool) (interface{}, string, error) {
	expected, expectedSource := parseExpectedToolNames(in.ExpectedOutput)
	actual, actualSource := parseActualToolNames(in.Output)
	if actualSource == "" {
		actual, actualSource = parseActualToolNames(in.Metadata)
		if actualSource != "" {
			actualSource = "metadata." + actualSource
		}
	}
	if actualSource == "" {
		if metadata, ok := inputMetadata(in.Input); ok {
			actual, actualSource = parseActualToolNames(metadata)
			if actualSource != "" {
				actualSource = "input.metadata." + actualSource
			}
		}
	}
	if expectedSource == "" {
		expectedSource = "none"
	}
	if actualSource == "" {
		actualSource = "none"
	}

	if len(expected) == 0 {
		score := 0.0
		if len(actual) == 0 {
			score = 1.0
		}
		return score, fmt.Sprintf(
			"expected tools empty; actual=%d extra_tools=[%s] expected_source=%s actual_source=%s ordered=%t",
			len(actual), formatToolList(sortedUniqueStrings(actual)), expectedSource, actualSource, ordered,
		), nil
	}

	if ordered {
		matched := lcsToolNames(actual, expected)
		missing := missingExpectedSequence(expected, matched)
		extra := extraToolNames(actual, expected)
		score := float64(len(matched)) / float64(len(expected))
		return score, fmt.Sprintf(
			"matched=%d/%d score=%.2f matched_tools=[%s] missing_tools=[%s] extra_tools=[%s] expected_source=%s actual_source=%s ordered=true",
			len(matched), len(expected), score, formatToolList(matched), formatToolList(missing), formatToolList(extra), expectedSource, actualSource,
		), nil
	}

	expectedSet := stringSet(expected)
	actualSet := stringSet(actual)
	matched := make([]string, 0, len(expectedSet))
	missing := make([]string, 0)
	for tool := range expectedSet {
		if _, ok := actualSet[tool]; ok {
			matched = append(matched, tool)
		} else {
			missing = append(missing, tool)
		}
	}
	extra := make([]string, 0)
	for tool := range actualSet {
		if _, ok := expectedSet[tool]; !ok {
			extra = append(extra, tool)
		}
	}
	sort.Strings(matched)
	sort.Strings(missing)
	sort.Strings(extra)
	score := float64(len(matched)) / float64(len(expectedSet))
	return score, fmt.Sprintf(
		"matched=%d/%d score=%.2f matched_tools=[%s] missing_tools=[%s] extra_tools=[%s] expected_source=%s actual_source=%s ordered=false",
		len(matched), len(expectedSet), score, formatToolList(matched), formatToolList(missing), formatToolList(extra), expectedSource, actualSource,
	), nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func parseExpectedToolNames(v interface{}) ([]string, string) {
	normalized, ok := normalizeJSONLike(v)
	if !ok {
		return nil, ""
	}
	switch t := normalized.(type) {
	case map[string]interface{}:
		if raw, exists := t["expected_tools"]; exists {
			if names, ok := toolNamesFromValue(raw); ok {
				return names, "expected_tools"
			}
		}
	case map[string]string:
		if raw, exists := t["expected_tools"]; exists {
			if names, ok := toolNamesFromValue(raw); ok {
				return names, "expected_tools"
			}
		}
	default:
		if names, ok := toolNamesFromValue(normalized); ok {
			return names, "array"
		}
	}
	return nil, ""
}

func parseActualToolNames(v interface{}) ([]string, string) {
	normalized, ok := normalizeJSONLike(v)
	if !ok {
		return nil, ""
	}
	switch t := normalized.(type) {
	case map[string]interface{}:
		for _, key := range []string{"tools_called", "tools"} {
			if raw, exists := t[key]; exists {
				if names, ok := toolNamesFromValue(raw); ok {
					return names, key
				}
			}
		}
	case map[string]string:
		for _, key := range []string{"tools_called", "tools"} {
			if raw, exists := t[key]; exists {
				if names, ok := toolNamesFromValue(raw); ok {
					return names, key
				}
			}
		}
	}
	if names, ok := toolNamesFromValue(normalized); ok {
		return names, "array"
	}
	return nil, ""
}

func normalizeJSONLike(v interface{}) (interface{}, bool) {
	if v == nil {
		return nil, false
	}
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, false
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(s), &decoded); err == nil {
			return decoded, true
		}
		return s, true
	case []byte:
		s := strings.TrimSpace(string(t))
		if s == "" {
			return nil, false
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(s), &decoded); err == nil {
			return decoded, true
		}
		return s, true
	default:
		return v, true
	}
}

func toolNamesFromValue(v interface{}) ([]string, bool) {
	normalized, ok := normalizeJSONLike(v)
	if !ok {
		return nil, false
	}
	switch t := normalized.(type) {
	case []interface{}:
		names := make([]string, 0, len(t))
		for _, item := range t {
			if name, ok := toolNameFromValue(item); ok {
				names = append(names, name)
			}
		}
		return names, true
	case []string:
		names := make([]string, 0, len(t))
		for _, item := range t {
			if name := strings.TrimSpace(item); name != "" {
				names = append(names, name)
			}
		}
		return names, true
	case []map[string]interface{}:
		names := make([]string, 0, len(t))
		for _, item := range t {
			if name, ok := toolNameFromObject(item); ok {
				names = append(names, name)
			}
		}
		return names, true
	case []map[string]string:
		names := make([]string, 0, len(t))
		for _, item := range t {
			if name, ok := toolNameFromStringObject(item); ok {
				names = append(names, name)
			}
		}
		return names, true
	case map[string]interface{}:
		if name, ok := toolNameFromObject(t); ok {
			return []string{name}, true
		}
	case map[string]string:
		if name, ok := toolNameFromStringObject(t); ok {
			return []string{name}, true
		}
	}
	return nil, false
}

func toolNameFromValue(v interface{}) (string, bool) {
	switch t := v.(type) {
	case string:
		name := strings.TrimSpace(t)
		return name, name != ""
	case map[string]interface{}:
		return toolNameFromObject(t)
	case map[string]string:
		return toolNameFromStringObject(t)
	default:
		normalized, ok := normalizeJSONLike(t)
		if !ok {
			return "", false
		}
		switch normalized.(type) {
		case string, map[string]interface{}, map[string]string:
			return toolNameFromValue(normalized)
		default:
			return "", false
		}
	}
}

func toolNameFromObject(m map[string]interface{}) (string, bool) {
	name, _ := m["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	return name, true
}

func toolNameFromStringObject(m map[string]string) (string, bool) {
	name := strings.TrimSpace(m["name"])
	if name == "" {
		return "", false
	}
	return name, true
}

func inputMetadata(input interface{}) (interface{}, bool) {
	normalized, ok := normalizeJSONLike(input)
	if !ok {
		return nil, false
	}
	switch t := normalized.(type) {
	case map[string]interface{}:
		metadata, ok := t["metadata"]
		return metadata, ok
	case map[string]string:
		metadata, ok := t["metadata"]
		return metadata, ok
	}
	return nil, false
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return set
}

func sortedUniqueStrings(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func extraToolNames(actual, expected []string) []string {
	expectedSet := stringSet(expected)
	extraSet := map[string]struct{}{}
	for _, tool := range actual {
		if _, ok := expectedSet[tool]; !ok {
			extraSet[tool] = struct{}{}
		}
	}
	extra := make([]string, 0, len(extraSet))
	for tool := range extraSet {
		extra = append(extra, tool)
	}
	sort.Strings(extra)
	return extra
}

func missingExpectedSequence(expected, matched []string) []string {
	missing := make([]string, 0)
	matchedIdx := 0
	for _, tool := range expected {
		if matchedIdx < len(matched) && tool == matched[matchedIdx] {
			matchedIdx++
			continue
		}
		missing = append(missing, tool)
	}
	return missing
}

func lcsToolNames(actual, expected []string) []string {
	if len(actual) == 0 || len(expected) == 0 {
		return nil
	}
	dp := make([][]int, len(actual)+1)
	for i := range dp {
		dp[i] = make([]int, len(expected)+1)
	}
	for i := len(actual) - 1; i >= 0; i-- {
		for j := len(expected) - 1; j >= 0; j-- {
			if actual[i] == expected[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	matched := make([]string, 0, dp[0][0])
	i, j := 0, 0
	for i < len(actual) && j < len(expected) {
		switch {
		case actual[i] == expected[j]:
			matched = append(matched, expected[j])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return matched
}

func formatToolList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func toStringForScore(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func paramFloat(params map[string]interface{}, key string) (float64, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func paramBool(params map[string]interface{}, key string) (bool, bool) {
	v, ok := params[key]
	if !ok {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(t)
		return b, err == nil
	case float64:
		return t != 0, true
	case int:
		return t != 0, true
	case int64:
		return t != 0, true
	}
	return false, false
}

// levenshtein returns the edit distance between a and b. O(n*m).
func levenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := 0; j <= len(br); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = minInt(
				curr[j-1]+1,    // insertion
				prev[j]+1,      // deletion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}
	return int(math.Round(float64(prev[len(br)])))
}

func minInt(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
