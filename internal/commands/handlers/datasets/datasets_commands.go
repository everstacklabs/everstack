package datasets

import (
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/google/uuid"
)

// --- Dataset Commands ---

// CreateDatasetCommand creates a new dataset.
type CreateDatasetCommand struct {
	commands.BaseCommand
	TenantID    string                 `json:"tenant_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

func NewCreateDatasetCommand(tenantID, name, description, userID, traceID string, metadata map[string]interface{}) *CreateDatasetCommand {
	return &CreateDatasetCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:    tenantID,
		Name:        name,
		Description: description,
		Metadata:    metadata,
	}
}

func (c CreateDatasetCommand) AggregateID() string { return c.ID }
func (c CreateDatasetCommand) CommandType() string { return "CreateDataset" }
func (c CreateDatasetCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// UpdateDatasetCommand updates an existing dataset.
type UpdateDatasetCommand struct {
	commands.BaseCommand
	DatasetID   string                 `json:"dataset_id"`
	TenantID    string                 `json:"tenant_id"`
	Name        *string                `json:"name,omitempty"`
	Description *string                `json:"description,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

func NewUpdateDatasetCommand(datasetID, tenantID, userID, traceID string) *UpdateDatasetCommand {
	return &UpdateDatasetCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		DatasetID: datasetID,
		TenantID:  tenantID,
	}
}

func (c UpdateDatasetCommand) AggregateID() string { return c.DatasetID }
func (c UpdateDatasetCommand) CommandType() string { return "UpdateDataset" }
func (c UpdateDatasetCommand) Validate() error {
	if c.DatasetID == "" {
		return fmt.Errorf("dataset_id cannot be empty")
	}
	return nil
}

// DeleteDatasetCommand deletes a dataset.
type DeleteDatasetCommand struct {
	commands.BaseCommand
	DatasetID string `json:"dataset_id"`
	TenantID  string `json:"tenant_id"`
}

func NewDeleteDatasetCommand(datasetID, tenantID, userID, traceID string) *DeleteDatasetCommand {
	return &DeleteDatasetCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		DatasetID: datasetID,
		TenantID:  tenantID,
	}
}

func (c DeleteDatasetCommand) AggregateID() string { return c.DatasetID }
func (c DeleteDatasetCommand) CommandType() string { return "DeleteDataset" }
func (c DeleteDatasetCommand) Validate() error {
	if c.DatasetID == "" {
		return fmt.Errorf("dataset_id cannot be empty")
	}
	return nil
}

// --- DatasetItem Commands ---

// CreateDatasetItemCommand creates a new dataset item.
type CreateDatasetItemCommand struct {
	commands.BaseCommand
	TenantID            string                 `json:"tenant_id"`
	DatasetID           string                 `json:"dataset_id"`
	Input               map[string]interface{} `json:"input"`
	ExpectedOutput      map[string]interface{} `json:"expected_output,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	SourceTraceID       string                 `json:"source_trace_id"`
	SourceObservationID string                 `json:"source_observation_id"`
}

func NewCreateDatasetItemCommand(
	tenantID, datasetID, userID, traceID string,
	input, expectedOutput, metadata map[string]interface{},
	sourceTraceID, sourceObservationID string,
) *CreateDatasetItemCommand {
	return &CreateDatasetItemCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:            tenantID,
		DatasetID:           datasetID,
		Input:               input,
		ExpectedOutput:      expectedOutput,
		Metadata:            metadata,
		SourceTraceID:       sourceTraceID,
		SourceObservationID: sourceObservationID,
	}
}

func (c CreateDatasetItemCommand) AggregateID() string { return c.ID }
func (c CreateDatasetItemCommand) CommandType() string { return "CreateDatasetItem" }
func (c CreateDatasetItemCommand) Validate() error {
	if c.DatasetID == "" {
		return fmt.Errorf("dataset_id cannot be empty")
	}
	if c.Input == nil || len(c.Input) == 0 {
		return fmt.Errorf("input cannot be empty")
	}
	return nil
}

// CreateDatasetItemBatchCommand creates multiple dataset items at once.
type CreateDatasetItemBatchCommand struct {
	commands.BaseCommand
	TenantID  string                     `json:"tenant_id"`
	DatasetID string                     `json:"dataset_id"`
	Items     []CreateDatasetItemCommand `json:"items"`
}

func NewCreateDatasetItemBatchCommand(tenantID, datasetID, userID, traceID string, items []CreateDatasetItemCommand) *CreateDatasetItemBatchCommand {
	return &CreateDatasetItemBatchCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:  tenantID,
		DatasetID: datasetID,
		Items:     items,
	}
}

func (c CreateDatasetItemBatchCommand) AggregateID() string { return c.ID }
func (c CreateDatasetItemBatchCommand) CommandType() string { return "CreateDatasetItemBatch" }
func (c CreateDatasetItemBatchCommand) Validate() error {
	if c.DatasetID == "" {
		return fmt.Errorf("dataset_id cannot be empty")
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("items cannot be empty")
	}
	return nil
}

// UpdateDatasetItemCommand updates an existing dataset item.
type UpdateDatasetItemCommand struct {
	commands.BaseCommand
	ItemID         string                 `json:"item_id"`
	TenantID       string                 `json:"tenant_id"`
	Input          map[string]interface{} `json:"input,omitempty"`
	ExpectedOutput map[string]interface{} `json:"expected_output,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Status         *string                `json:"status,omitempty"`
}

func NewUpdateDatasetItemCommand(itemID, tenantID, userID, traceID string) *UpdateDatasetItemCommand {
	return &UpdateDatasetItemCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ItemID:   itemID,
		TenantID: tenantID,
	}
}

func (c UpdateDatasetItemCommand) AggregateID() string { return c.ItemID }
func (c UpdateDatasetItemCommand) CommandType() string { return "UpdateDatasetItem" }
func (c UpdateDatasetItemCommand) Validate() error {
	if c.ItemID == "" {
		return fmt.Errorf("item_id cannot be empty")
	}
	return nil
}

// DeleteDatasetItemCommand deletes a dataset item.
type DeleteDatasetItemCommand struct {
	commands.BaseCommand
	ItemID   string `json:"item_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeleteDatasetItemCommand(itemID, tenantID, userID, traceID string) *DeleteDatasetItemCommand {
	return &DeleteDatasetItemCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ItemID:   itemID,
		TenantID: tenantID,
	}
}

func (c DeleteDatasetItemCommand) AggregateID() string { return c.ItemID }
func (c DeleteDatasetItemCommand) CommandType() string { return "DeleteDatasetItem" }
func (c DeleteDatasetItemCommand) Validate() error {
	if c.ItemID == "" {
		return fmt.Errorf("item_id cannot be empty")
	}
	return nil
}

// --- ScoreConfig Commands ---

type ScoreConfigMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ScoreConfigModelParamsPayload struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int32   `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	ToolChoice  *string  `json:"tool_choice,omitempty"`
}

type ScoreConfigChoiceScorePayload struct {
	Choice string  `json:"choice"`
	Score  float64 `json:"score"`
}

// CreateScoreConfigCommand creates a new score config.
type CreateScoreConfigCommand struct {
	commands.BaseCommand
	TenantID       string                          `json:"tenant_id"`
	Name           string                          `json:"name"`
	DataType       string                          `json:"data_type"`
	Description    string                          `json:"description"`
	MinValue       *float64                        `json:"min_value,omitempty"`
	MaxValue       *float64                        `json:"max_value,omitempty"`
	Categories     map[string]interface{}          `json:"categories,omitempty"`
	EvalPrompt     string                          `json:"eval_prompt"`
	EvalModel      string                          `json:"eval_model"`
	ScorerCode     string                          `json:"scorer_code"`
	ScorerLanguage string                          `json:"scorer_language"`
	UseSandbox     bool                            `json:"use_sandbox"`
	Slug           string                          `json:"slug"`
	ScorerType     string                          `json:"scorer_type"`
	OutputType     string                          `json:"output_type"`
	Messages       []ScoreConfigMessagePayload     `json:"messages,omitempty"`
	ModelParams    *ScoreConfigModelParamsPayload  `json:"model_params,omitempty"`
	ChoiceScores   []ScoreConfigChoiceScorePayload `json:"choice_scores,omitempty"`
	UseCot         bool                            `json:"use_cot"`
	PassThreshold  *float64                        `json:"pass_threshold,omitempty"`
	DagDefinition  []byte                          `json:"dag_definition,omitempty"`
}

func NewCreateScoreConfigCommand(
	tenantID, name, dataType, description, userID, traceID string,
	minValue, maxValue *float64,
	categories map[string]interface{},
	evalPrompt, evalModel string,
	scorerCode, scorerLanguage string,
	useSandbox bool,
	slug, scorerType, outputType string,
	messages []ScoreConfigMessagePayload,
	modelParams *ScoreConfigModelParamsPayload,
	choiceScores []ScoreConfigChoiceScorePayload,
	useCot bool,
	passThreshold *float64,
	dagDefinition []byte,
) *CreateScoreConfigCommand {
	return &CreateScoreConfigCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:       tenantID,
		Name:           name,
		DataType:       dataType,
		Description:    description,
		MinValue:       minValue,
		MaxValue:       maxValue,
		Categories:     categories,
		EvalPrompt:     evalPrompt,
		EvalModel:      evalModel,
		ScorerCode:     scorerCode,
		ScorerLanguage: scorerLanguage,
		UseSandbox:     useSandbox,
		Slug:           slug,
		ScorerType:     scorerType,
		OutputType:     outputType,
		Messages:       messages,
		ModelParams:    modelParams,
		ChoiceScores:   choiceScores,
		UseCot:         useCot,
		PassThreshold:  passThreshold,
		DagDefinition:  dagDefinition,
	}
}

func (c CreateScoreConfigCommand) AggregateID() string { return c.ID }
func (c CreateScoreConfigCommand) CommandType() string { return "CreateScoreConfig" }
func (c CreateScoreConfigCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	dt := strings.ToUpper(c.DataType)
	// LLM_JUDGE is the FE label for prompt-driven scorers (eval_prompt +
	// eval_model fields); the runner already understands it (see
	// internal/services/eval_runner/scorer.go).
	if dt != "NUMERIC" && dt != "CATEGORICAL" && dt != "BOOLEAN" && dt != "CODE_SCORER" && dt != "LLM_JUDGE" {
		return fmt.Errorf("invalid data_type: %s (must be NUMERIC, CATEGORICAL, BOOLEAN, CODE_SCORER, or LLM_JUDGE)", c.DataType)
	}
	return nil
}

// UpdateScoreConfigCommand updates an existing score config.
type UpdateScoreConfigCommand struct {
	commands.BaseCommand
	ScoreConfigID  string                          `json:"score_config_id"`
	TenantID       string                          `json:"tenant_id"`
	Name           *string                         `json:"name,omitempty"`
	Description    *string                         `json:"description,omitempty"`
	MinValue       *float64                        `json:"min_value,omitempty"`
	MaxValue       *float64                        `json:"max_value,omitempty"`
	Categories     map[string]interface{}          `json:"categories,omitempty"`
	EvalPrompt     *string                         `json:"eval_prompt,omitempty"`
	EvalModel      *string                         `json:"eval_model,omitempty"`
	IsArchived     *bool                           `json:"is_archived,omitempty"`
	ScorerCode     *string                         `json:"scorer_code,omitempty"`
	ScorerLanguage *string                         `json:"scorer_language,omitempty"`
	UseSandbox     *bool                           `json:"use_sandbox,omitempty"`
	DataType       *string                         `json:"data_type,omitempty"`
	Slug           *string                         `json:"slug,omitempty"`
	ScorerType     *string                         `json:"scorer_type,omitempty"`
	OutputType     *string                         `json:"output_type,omitempty"`
	Messages       []ScoreConfigMessagePayload     `json:"messages,omitempty"`
	ModelParams    *ScoreConfigModelParamsPayload  `json:"model_params,omitempty"`
	ChoiceScores   []ScoreConfigChoiceScorePayload `json:"choice_scores,omitempty"`
	UseCot         *bool                           `json:"use_cot,omitempty"`
	PassThreshold  *float64                        `json:"pass_threshold,omitempty"`
	DagDefinition  []byte                          `json:"dag_definition,omitempty"`
}

func NewUpdateScoreConfigCommand(scoreConfigID, tenantID, userID, traceID string) *UpdateScoreConfigCommand {
	return &UpdateScoreConfigCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ScoreConfigID: scoreConfigID,
		TenantID:      tenantID,
	}
}

func (c UpdateScoreConfigCommand) AggregateID() string { return c.ScoreConfigID }
func (c UpdateScoreConfigCommand) CommandType() string { return "UpdateScoreConfig" }
func (c UpdateScoreConfigCommand) Validate() error {
	if c.ScoreConfigID == "" {
		return fmt.Errorf("score_config_id cannot be empty")
	}
	return nil
}

// DeleteScoreConfigCommand deletes a score config.
type DeleteScoreConfigCommand struct {
	commands.BaseCommand
	ScoreConfigID string `json:"score_config_id"`
	TenantID      string `json:"tenant_id"`
}

func NewDeleteScoreConfigCommand(scoreConfigID, tenantID, userID, traceID string) *DeleteScoreConfigCommand {
	return &DeleteScoreConfigCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ScoreConfigID: scoreConfigID,
		TenantID:      tenantID,
	}
}

func (c DeleteScoreConfigCommand) AggregateID() string { return c.ScoreConfigID }
func (c DeleteScoreConfigCommand) CommandType() string { return "DeleteScoreConfig" }
func (c DeleteScoreConfigCommand) Validate() error {
	if c.ScoreConfigID == "" {
		return fmt.Errorf("score_config_id cannot be empty")
	}
	return nil
}

// --- EvalRun Commands ---

// CreateEvalRunCommand creates a new eval run.
type CreateEvalRunCommand struct {
	commands.BaseCommand
	TenantID         string                 `json:"tenant_id"`
	DatasetID        string                 `json:"dataset_id"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	EvalTargetType   string                 `json:"eval_target_type"`
	EvalTargetID     string                 `json:"eval_target_id"`
	EvalConfig       map[string]interface{} `json:"eval_config,omitempty"`
	ScorerConfigIDs  []string               `json:"scorer_config_ids,omitempty"`
	DatasetVersionID string                 `json:"dataset_version_id,omitempty"`
}

func NewCreateEvalRunCommand(
	tenantID, datasetID, name, description, evalTargetType, evalTargetID, userID, traceID string,
	evalConfig map[string]interface{},
	scorerConfigIDs []string,
	datasetVersionID string,
) *CreateEvalRunCommand {
	return &CreateEvalRunCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:         tenantID,
		DatasetID:        datasetID,
		Name:             name,
		Description:      description,
		EvalTargetType:   evalTargetType,
		EvalTargetID:     evalTargetID,
		EvalConfig:       evalConfig,
		ScorerConfigIDs:  scorerConfigIDs,
		DatasetVersionID: datasetVersionID,
	}
}

func (c CreateEvalRunCommand) AggregateID() string { return c.ID }
func (c CreateEvalRunCommand) CommandType() string { return "CreateEvalRun" }
func (c CreateEvalRunCommand) Validate() error {
	if c.DatasetID == "" {
		return fmt.Errorf("dataset_id cannot be empty")
	}
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// CancelEvalRunCommand cancels a running eval run.
type CancelEvalRunCommand struct {
	commands.BaseCommand
	EvalRunID string `json:"eval_run_id"`
	TenantID  string `json:"tenant_id"`
}

func NewCancelEvalRunCommand(evalRunID, tenantID, userID, traceID string) *CancelEvalRunCommand {
	return &CancelEvalRunCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		EvalRunID: evalRunID,
		TenantID:  tenantID,
	}
}

func (c CancelEvalRunCommand) AggregateID() string { return c.EvalRunID }
func (c CancelEvalRunCommand) CommandType() string { return "CancelEvalRun" }
func (c CancelEvalRunCommand) Validate() error {
	if c.EvalRunID == "" {
		return fmt.Errorf("eval_run_id cannot be empty")
	}
	return nil
}

// DeleteEvalRunCommand deletes an eval run.
type DeleteEvalRunCommand struct {
	commands.BaseCommand
	EvalRunID string `json:"eval_run_id"`
	TenantID  string `json:"tenant_id"`
}

func NewDeleteEvalRunCommand(evalRunID, tenantID, userID, traceID string) *DeleteEvalRunCommand {
	return &DeleteEvalRunCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		EvalRunID: evalRunID,
		TenantID:  tenantID,
	}
}

func (c DeleteEvalRunCommand) AggregateID() string { return c.EvalRunID }
func (c DeleteEvalRunCommand) CommandType() string { return "DeleteEvalRun" }
func (c DeleteEvalRunCommand) Validate() error {
	if c.EvalRunID == "" {
		return fmt.Errorf("eval_run_id cannot be empty")
	}
	return nil
}
