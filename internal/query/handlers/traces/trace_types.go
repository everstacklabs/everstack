package traces

import "time"

// EverstackTrace represents a trace in Everstack format
type EverstackTrace struct {
	ID           string                `json:"id"`
	Timestamp    time.Time             `json:"timestamp"`
	Name         *string               `json:"name,omitempty"`
	Input        interface{}           `json:"input,omitempty"`
	Output       interface{}           `json:"output,omitempty"`
	SessionID    *string               `json:"sessionId,omitempty"`
	Release      *string               `json:"release,omitempty"`
	Version      *string               `json:"version,omitempty"`
	UserID       *string               `json:"userId,omitempty"`
	Metadata     interface{}           `json:"metadata,omitempty"`
	Tags         []string              `json:"tags,omitempty"`
	Public       *bool                 `json:"public,omitempty"`
	Environment  *string               `json:"environment,omitempty"`
	HTMLPath     string                `json:"htmlPath"`
	Latency      float64               `json:"latency"` // in seconds
	TotalCost    float64               `json:"totalCost"`
	Observations []EverstackObservation `json:"observations"`
	Scores       []EverstackScore       `json:"scores"`
}

// EverstackObservation represents a span/observation in Everstack format
type EverstackObservation struct {
	ID                  string                 `json:"id"`
	TraceID             *string                `json:"traceId,omitempty"`
	Type                string                 `json:"type"` // SPAN, GENERATION, EVENT
	Name                *string                `json:"name,omitempty"`
	StartTime           time.Time              `json:"startTime"`
	EndTime             *time.Time             `json:"endTime,omitempty"`
	CompletionStartTime *time.Time             `json:"completionStartTime,omitempty"`
	Model               *string                `json:"model,omitempty"`
	ModelParameters     map[string]interface{} `json:"modelParameters,omitempty"`
	Input               interface{}            `json:"input,omitempty"`
	Version             *string                `json:"version,omitempty"`
	Metadata            interface{}            `json:"metadata,omitempty"`
	Output              interface{}            `json:"output,omitempty"`
	Usage               *EverstackUsage         `json:"usage,omitempty"`
	Level               string                 `json:"level"` // DEBUG, DEFAULT, WARNING, ERROR
	StatusMessage       *string                `json:"statusMessage,omitempty"`
	ParentObservationID *string                `json:"parentObservationId,omitempty"`
	PromptID            *string                `json:"promptId,omitempty"`
	UsageDetails        map[string]int64       `json:"usageDetails,omitempty"`
	CostDetails         map[string]float64     `json:"costDetails,omitempty"`
	Environment         *string                `json:"environment,omitempty"`

	// Calculated fields
	PromptName           *string  `json:"promptName,omitempty"`
	PromptVersion        *int     `json:"promptVersion,omitempty"`
	ModelID              *string  `json:"modelId,omitempty"`
	InputPrice           *float64 `json:"inputPrice,omitempty"`
	OutputPrice          *float64 `json:"outputPrice,omitempty"`
	TotalPrice           *float64 `json:"totalPrice,omitempty"`
	CalculatedInputCost  *float64 `json:"calculatedInputCost,omitempty"`
	CalculatedOutputCost *float64 `json:"calculatedOutputCost,omitempty"`
	CalculatedTotalCost  *float64 `json:"calculatedTotalCost,omitempty"`
	Latency              *float64 `json:"latency,omitempty"`          // in seconds
	TimeToFirstToken     *float64 `json:"timeToFirstToken,omitempty"` // in seconds
}

// EverstackUsage represents token usage (deprecated in favor of usageDetails)
type EverstackUsage struct {
	Input      *int64   `json:"input,omitempty"`
	Output     *int64   `json:"output,omitempty"`
	Total      *int64   `json:"total,omitempty"`
	Unit       *string  `json:"unit,omitempty"` // TOKENS, CHARACTERS, etc.
	InputCost  *float64 `json:"inputCost,omitempty"`
	OutputCost *float64 `json:"outputCost,omitempty"`
	TotalCost  *float64 `json:"totalCost,omitempty"`
}

// EverstackScore represents an evaluation score
type EverstackScore struct {
	ID            string      `json:"id"`
	TraceID       string      `json:"traceId"`
	Name          string      `json:"name"`
	Source        string      `json:"source"` // ANNOTATION, API, EVAL
	ObservationID *string     `json:"observationId,omitempty"`
	Timestamp     time.Time   `json:"timestamp"`
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
	AuthorUserID  *string     `json:"authorUserId,omitempty"`
	Comment       *string     `json:"comment,omitempty"`
	Metadata      interface{} `json:"metadata,omitempty"`
	ConfigID      *string     `json:"configId,omitempty"`
	QueueID       *string     `json:"queueId,omitempty"`
	Environment   *string     `json:"environment,omitempty"`
	DataType      string      `json:"dataType"` // NUMERIC, CATEGORICAL, BOOLEAN

	// Score values (only one populated based on dataType)
	Value       *float64 `json:"value,omitempty"`       // For NUMERIC and BOOLEAN
	StringValue *string  `json:"stringValue,omitempty"` // For CATEGORICAL and BOOLEAN
}

// TraceFilter represents filter criteria for trace queries
type TraceFilter struct {
	TraceIDs    []string
	SessionID   *string
	UserID      *string
	ThreadID    *string
	Environment *string
	Tags        []string
	FromTime    *time.Time
	ToTime      *time.Time
	Model       *string
	Provider    *string
	Limit       int
	Offset      int
}
