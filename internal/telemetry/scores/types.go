package scores

import "time"

// ScoreDataType represents the type of score value
type ScoreDataType string

const (
	ScoreDataTypeNumeric     ScoreDataType = "NUMERIC"
	ScoreDataTypeCategorical ScoreDataType = "CATEGORICAL"
	ScoreDataTypeBoolean     ScoreDataType = "BOOLEAN"
)

// ScoreSource represents where the score originated from
type ScoreSource string

const (
	ScoreSourceAnnotation ScoreSource = "ANNOTATION"
	ScoreSourceAPI        ScoreSource = "API"
	ScoreSourceEval       ScoreSource = "EVAL"
)

// Score represents a quality/evaluation score for a trace or observation
type Score struct {
	ID            string    // Unique score identifier
	TraceID       string    // Required: Trace this score belongs to
	ObservationID string    // Optional: Specific span/observation within trace
	Timestamp     time.Time // When the score was recorded
	CreatedAt     time.Time
	UpdatedAt     time.Time

	// Score metadata
	Name     string      // Score name (e.g., "quality", "accuracy", "coherence")
	Source   ScoreSource // How this score was created
	DataType ScoreDataType

	// Score values (only one should be populated based on DataType)
	NumericValue *float64 // For NUMERIC scores
	StringValue  *string  // For CATEGORICAL scores
	BooleanValue *bool    // For BOOLEAN scores (also stored as 1/0 in NumericValue)

	// Additional metadata
	Comment      string                 // Optional human-readable comment
	AuthorUserID string                 // User who created the score
	ConfigID     string                 // Reference to score config (for validation)
	QueueID      string                 // Reference to annotation queue
	Metadata     map[string]interface{} // Additional metadata
	Environment  string                 // Environment from which score originated
}

// NumericScore is a convenience constructor for numeric scores
func NumericScore(traceID, name string, value float64, source ScoreSource) *Score {
	now := time.Now()
	return &Score{
		TraceID:      traceID,
		Name:         name,
		Source:       source,
		DataType:     ScoreDataTypeNumeric,
		NumericValue: &value,
		Timestamp:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// CategoricalScore is a convenience constructor for categorical scores
func CategoricalScore(traceID, name, stringValue string, source ScoreSource) *Score {
	now := time.Now()
	return &Score{
		TraceID:     traceID,
		Name:        name,
		Source:      source,
		DataType:    ScoreDataTypeCategorical,
		StringValue: &stringValue,
		Timestamp:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// BooleanScore is a convenience constructor for boolean scores
func BooleanScore(traceID, name string, value bool, source ScoreSource) *Score {
	now := time.Now()
	numValue := 0.0
	if value {
		numValue = 1.0
	}

	return &Score{
		TraceID:      traceID,
		Name:         name,
		Source:       source,
		DataType:     ScoreDataTypeBoolean,
		BooleanValue: &value,
		NumericValue: &numValue, // Also store as numeric for queries
		Timestamp:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// GetNumericValue returns the numeric representation of the score
// For boolean scores, returns 1.0 for true, 0.0 for false
func (s *Score) GetNumericValue() (float64, bool) {
	if s.NumericValue != nil {
		return *s.NumericValue, true
	}
	if s.BooleanValue != nil {
		if *s.BooleanValue {
			return 1.0, true
		}
		return 0.0, true
	}
	return 0, false
}

// GetStringValue returns the string representation of the score
// For boolean scores, returns "True" or "False"
func (s *Score) GetStringValue() (string, bool) {
	if s.StringValue != nil {
		return *s.StringValue, true
	}
	if s.BooleanValue != nil {
		if *s.BooleanValue {
			return "True", true
		}
		return "False", true
	}
	return "", false
}

// Validate checks if the score is valid
func (s *Score) Validate() error {
	if s.TraceID == "" {
		return ErrMissingTraceID
	}
	if s.Name == "" {
		return ErrMissingScoreName
	}

	// Validate that appropriate value is set for data type
	switch s.DataType {
	case ScoreDataTypeNumeric:
		if s.NumericValue == nil {
			return ErrMissingNumericValue
		}
	case ScoreDataTypeCategorical:
		if s.StringValue == nil {
			return ErrMissingStringValue
		}
	case ScoreDataTypeBoolean:
		if s.BooleanValue == nil {
			return ErrMissingBooleanValue
		}
	default:
		return ErrInvalidDataType
	}

	return nil
}

// Common errors
var (
	ErrMissingTraceID      = &ScoreError{Message: "trace_id is required"}
	ErrMissingScoreName    = &ScoreError{Message: "score name is required"}
	ErrMissingNumericValue = &ScoreError{Message: "numeric value required for NUMERIC score"}
	ErrMissingStringValue  = &ScoreError{Message: "string value required for CATEGORICAL score"}
	ErrMissingBooleanValue = &ScoreError{Message: "boolean value required for BOOLEAN score"}
	ErrInvalidDataType     = &ScoreError{Message: "invalid score data type"}
)

// ScoreError represents a score-related error
type ScoreError struct {
	Message string
}

func (e *ScoreError) Error() string {
	return e.Message
}

