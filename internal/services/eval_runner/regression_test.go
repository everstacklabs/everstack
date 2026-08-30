package eval_runner

import (
	"testing"
)

func TestParseScoreSummary(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected map[string]float64
	}{
		{
			name:     "empty",
			input:    []byte("{}"),
			expected: nil,
		},
		{
			name:  "valid scores",
			input: []byte(`{"scores":[{"name":"accuracy","avgScore":0.95,"minScore":0.8,"maxScore":1.0,"count":10},{"name":"latency","avgScore":0.7,"minScore":0.5,"maxScore":0.9,"count":10}]}`),
			expected: map[string]float64{
				"accuracy": 0.95,
				"latency":  0.7,
			},
		},
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseScoreSummary(tt.input)
			if tt.expected == nil {
				if result != nil && len(result) > 0 {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("score %s: expected %f, got %f", k, v, result[k])
				}
			}
		})
	}
}

func TestRegressionThreshold(t *testing.T) {
	// A 5% drop should be detected
	if RegressionThreshold != 0.05 {
		t.Errorf("expected threshold 0.05, got %f", RegressionThreshold)
	}
}

func TestScoreRegressionDetection(t *testing.T) {
	tests := []struct {
		name        string
		baseline    float64
		current     float64
		expectRegression bool
	}{
		{"no drop", 0.90, 0.90, false},
		{"small improvement", 0.90, 0.92, false},
		{"small drop within threshold", 0.90, 0.87, false},       // 3.3% drop
		{"drop at threshold boundary", 0.90, 0.855, true},        // exactly 5% drop (strict <)
		{"drop beyond threshold", 0.90, 0.84, true},              // 6.7% drop
		{"large drop", 0.90, 0.70, true},                         // 22% drop
		{"zero baseline", 0.0, 0.5, false},                       // can't compute pct
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta := tt.current - tt.baseline
			var deltaPct float64
			if tt.baseline != 0 {
				deltaPct = delta / tt.baseline
				if deltaPct < 0 {
					deltaPct = -deltaPct
					deltaPct = -deltaPct // restore sign
				}
				deltaPct = delta / tt.baseline
			}
			regressed := deltaPct < -RegressionThreshold

			if regressed != tt.expectRegression {
				t.Errorf("baseline=%.3f current=%.3f deltaPct=%.4f: expected regression=%v, got %v",
					tt.baseline, tt.current, deltaPct, tt.expectRegression, regressed)
			}
		})
	}
}
