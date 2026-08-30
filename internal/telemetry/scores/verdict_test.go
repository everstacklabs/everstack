package scores

import "testing"

func TestFixAttemptVerdictScore(t *testing.T) {
	for _, v := range FixAttemptVerdicts {
		s, err := FixAttemptVerdictScore("trace-1", v, ScoreSourceAPI)
		if err != nil {
			t.Fatalf("verdict %q: unexpected err %v", v, err)
		}
		if s.Name != FixAttemptVerdictName {
			t.Fatalf("verdict %q: wrong name %s", v, s.Name)
		}
		if s.DataType != ScoreDataTypeCategorical {
			t.Fatalf("verdict %q: wrong data type %s", v, s.DataType)
		}
		if s.StringValue == nil || *s.StringValue != v {
			t.Fatalf("verdict %q: wrong string value %v", v, s.StringValue)
		}
	}
}

func TestFixAttemptVerdictScore_RejectsUnknown(t *testing.T) {
	if _, err := FixAttemptVerdictScore("trace-1", "maybe", ScoreSourceAPI); err == nil {
		t.Fatal("expected error for unknown verdict, got nil")
	}
}

func TestValidateFixAttemptVerdict_PassesNonReservedName(t *testing.T) {
	s := CategoricalScore("trace-1", "user.sentiment", "happy", ScoreSourceAPI)
	if err := ValidateFixAttemptVerdict(s); err != nil {
		t.Fatalf("non-reserved name should pass, got %v", err)
	}
}

func TestValidateFixAttemptVerdict_RejectsBadValue(t *testing.T) {
	bad := "kinda-won"
	s := &Score{
		TraceID:     "trace-1",
		Name:        FixAttemptVerdictName,
		DataType:    ScoreDataTypeCategorical,
		StringValue: &bad,
	}
	if err := ValidateFixAttemptVerdict(s); err == nil {
		t.Fatal("expected error for bad verdict value, got nil")
	}
}

func TestValidateFixAttemptVerdict_RejectsWrongType(t *testing.T) {
	val := 1.0
	s := &Score{
		TraceID:      "trace-1",
		Name:         FixAttemptVerdictName,
		DataType:     ScoreDataTypeNumeric,
		NumericValue: &val,
	}
	if err := ValidateFixAttemptVerdict(s); err == nil {
		t.Fatal("expected error for numeric data type, got nil")
	}
}
