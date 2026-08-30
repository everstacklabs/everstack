package eval_runner

import (
	"math"
	"strings"
	"testing"
)

func TestBuiltinToolCorrectness(t *testing.T) {
	t.Run("exact match scores one", func(t *testing.T) {
		score, _ := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`{"tools_called":["search","read_file"]}`,
			`{"expected_tools":["search","read_file"]}`,
			nil,
		)

		assertFloatScore(t, score, 1.0)
	})

	t.Run("partial match scores fraction", func(t *testing.T) {
		score, _ := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`{"tools_called":["search"]}`,
			`{"expected_tools":["search","read_file","edit_file"]}`,
			nil,
		)

		assertFloatScore(t, score, 1.0/3.0)
	})

	t.Run("reports missing and extra tools", func(t *testing.T) {
		score, reason := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`{"tools_called":["search","unknown_tool"]}`,
			`{"expected_tools":["search","edit_file"]}`,
			nil,
		)

		assertFloatScore(t, score, 0.5)
		if !strings.Contains(reason, "missing_tools=[edit_file]") {
			t.Fatalf("expected missing tool in reason, got %q", reason)
		}
		if !strings.Contains(reason, "extra_tools=[unknown_tool]") {
			t.Fatalf("expected extra tool in reason, got %q", reason)
		}
	})

	t.Run("empty expected rewards no actual tools", func(t *testing.T) {
		score, _ := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`{"tools_called":[]}`,
			`{"expected_tools":[]}`,
			nil,
		)

		assertFloatScore(t, score, 1.0)
	})

	t.Run("empty expected penalizes unexpected actual tools", func(t *testing.T) {
		score, _ := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`{"tools_called":["search"]}`,
			`{"expected_tools":[]}`,
			nil,
		)

		assertFloatScore(t, score, 0.0)
	})

	t.Run("metadata fallback when output has no tool list", func(t *testing.T) {
		score, reason := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`{"message":"done"}`,
			`{"expected_tools":["search"]}`,
			map[string]interface{}{"tools_called": []interface{}{"search"}},
		)

		assertFloatScore(t, score, 1.0)
		if !strings.Contains(reason, "actual_source=metadata.tools_called") {
			t.Fatalf("expected metadata source in reason, got %q", reason)
		}
	})

	t.Run("bare array expected and object array actual", func(t *testing.T) {
		score, _ := runToolCorrectnessScore(t, "builtin_tool_correctness",
			`[{"name":"search"},{"name":"write_file"}]`,
			`["search","write_file"]`,
			nil,
		)

		assertFloatScore(t, score, 1.0)
	})
}

func TestBuiltinToolCorrectnessOrdered(t *testing.T) {
	t.Run("ordered subsequence scores one", func(t *testing.T) {
		score, _ := runToolCorrectnessScore(t, "builtin_tool_correctness_ordered",
			`{"tools_called":["plan","search","read_file","edit_file"]}`,
			`{"expected_tools":["search","edit_file"]}`,
			nil,
		)

		assertFloatScore(t, score, 1.0)
	})

	t.Run("out of order scores longest common subsequence fraction", func(t *testing.T) {
		score, reason := runToolCorrectnessScore(t, "builtin_tool_correctness_ordered",
			`{"tools_called":["edit_file","search"]}`,
			`{"expected_tools":["search","edit_file"]}`,
			nil,
		)

		assertFloatScore(t, score, 0.5)
		if !strings.Contains(reason, "ordered=true") {
			t.Fatalf("expected ordered reason, got %q", reason)
		}
	})
}

func TestBuiltinAgenticConversationalMetricsPresent(t *testing.T) {
	want := map[string]string{
		"conversation_completeness": "conversational",
		"role_adherence":            "conversational",
		"knowledge_retention":       "conversational",
		"turn_relevancy":            "conversational",
		"task_completion":           "agentic",
		"plan_adherence":            "agentic",
	}
	seen := map[string]MetricTemplate{}
	for _, metric := range BuiltinMetrics {
		seen[metric.Key] = metric
	}

	for key, category := range want {
		metric, ok := seen[key]
		if !ok {
			t.Fatalf("missing builtin metric %q", key)
		}
		if metric.DataType != "NUMERIC" {
			t.Fatalf("metric %q DataType=%q, want NUMERIC", key, metric.DataType)
		}
		if metric.Category != category {
			t.Fatalf("metric %q Category=%q, want %q", key, metric.Category, category)
		}
		if strings.TrimSpace(metric.EvalPrompt) == "" {
			t.Fatalf("metric %q has empty EvalPrompt", key)
		}
		if metric.MinValue != 0 || metric.MaxValue != 1 || metric.MinValue >= metric.MaxValue {
			t.Fatalf("metric %q has invalid bounds min=%v max=%v", key, metric.MinValue, metric.MaxValue)
		}
	}
}

func runToolCorrectnessScore(t *testing.T, dataType string, output, expectedOutput, metadata interface{}) (float64, string) {
	t.Helper()

	r := &Runner{}
	cfg := ScoreConfig{
		ID:       "cfg-tool-correctness",
		Name:     "ToolCorrectness",
		DataType: dataType,
	}
	scores := r.ScoreOutput(nil, "tenant-1", "tool-correctness-test", ScoreInput{
		Output:         output,
		ExpectedOutput: expectedOutput,
		Metadata:       metadata,
	}, []ScoreConfig{cfg})

	if errValue, ok := scores["ToolCorrectness_error"]; ok {
		t.Fatalf("did not expect scorer error: %v", errValue)
	}
	got, ok := scores["ToolCorrectness"].(float64)
	if !ok {
		t.Fatalf("expected float64 score, got %#v in scores %#v", scores["ToolCorrectness"], scores)
	}
	reason, _ := scores["ToolCorrectness_reason"].(string)
	if reason == "" {
		t.Fatalf("expected non-empty reason, got scores %#v", scores)
	}
	return got, reason
}

func assertFloatScore(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("score=%v, want %v", got, want)
	}
}
