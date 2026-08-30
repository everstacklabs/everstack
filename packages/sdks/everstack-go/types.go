package everstack

// ---------------------------------------------------------------------------
// Chat types
// ---------------------------------------------------------------------------

// Message represents a chat message.
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ChatCompletionParams are the parameters for creating a chat completion.
type ChatCompletionParams struct {
	Model            string    `json:"model"`
	Messages         []Message `json:"messages"`
	Temperature      *float64  `json:"temperature,omitempty"`
	TopP             *float64  `json:"top_p,omitempty"`
	MaxTokens        *int      `json:"max_tokens,omitempty"`
	Stop             []string  `json:"stop,omitempty"`
	FrequencyPenalty *float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64  `json:"presence_penalty,omitempty"`
	N                *int      `json:"n,omitempty"`
	Stream           bool      `json:"stream,omitempty"`
	Tools            []Tool    `json:"tools,omitempty"`
	ToolChoice       any       `json:"tool_choice,omitempty"`
}

// Tool represents a tool the model can call.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a function tool.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// Usage contains token usage information.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ToolCall represents a tool call in a response.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function details of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChoiceMessage is the message in a chat completion choice.
type ChoiceMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int           `json:"index"`
	Message      ChoiceMessage `json:"message"`
	FinishReason *string       `json:"finish_reason"`
}

// FallbackInfo contains details about model fallback.
type FallbackInfo struct {
	FallbackUsed     bool    `json:"fallback_used"`
	RequestedModel   string  `json:"requested_model"`
	ActualModel      string  `json:"actual_model"`
	FallbackReason   *string `json:"fallback_reason,omitempty"`
	FallbackAttempts *int    `json:"fallback_attempts,omitempty"`
}

// ChatCompletionResponse is the response from a chat completion request.
type ChatCompletionResponse struct {
	ID           string        `json:"id"`
	Object       string        `json:"object"`
	Created      int64         `json:"created"`
	Model        string        `json:"model"`
	Choices      []Choice      `json:"choices"`
	Usage        Usage         `json:"usage"`
	FallbackInfo *FallbackInfo `json:"fallback_info,omitempty"`
}

// DeltaMessage is the incremental message in a streaming chunk.
type DeltaMessage struct {
	Role    *string `json:"role,omitempty"`
	Content *string `json:"content,omitempty"`
}

// ChunkChoice is a single choice in a streaming chunk.
type ChunkChoice struct {
	Index        int          `json:"index"`
	Delta        DeltaMessage `json:"delta"`
	FinishReason *string      `json:"finish_reason,omitempty"`
}

// ChatCompletionChunk is a streaming chunk from a chat completion.
type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

// ---------------------------------------------------------------------------
// Embeddings types
// ---------------------------------------------------------------------------

// EmbeddingsParams are the parameters for creating embeddings.
type EmbeddingsParams struct {
	Model          string `json:"model"`
	Input          any    `json:"input"` // string or []string
	EncodingFormat string `json:"encoding_format,omitempty"`
	Dimensions     *int   `json:"dimensions,omitempty"`
}

// EmbeddingData is a single embedding vector.
type EmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// EmbeddingsUsage contains embedding token usage.
type EmbeddingsUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// EmbeddingsResponse is the response from an embeddings request.
type EmbeddingsResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  EmbeddingsUsage `json:"usage"`
}

// ---------------------------------------------------------------------------
// Models types
// ---------------------------------------------------------------------------

// ModelInfo describes a single model.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsListResponse is the response from listing models.
type ModelsListResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ---------------------------------------------------------------------------
// Audio types
// ---------------------------------------------------------------------------

// SpeechParams are the parameters for text-to-speech.
type SpeechParams struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
}

// SpeechResponse is the response from a speech request.
type SpeechResponse struct {
	Audio            []byte  `json:"audio"`
	Format           string  `json:"format"`
	ContentType      string  `json:"content_type"`
	DurationSeconds  float64 `json:"duration_seconds"`
	InputCharacters  int     `json:"input_characters"`
}

// TranscriptionParams are the parameters for audio transcription.
type TranscriptionParams struct {
	Model          string   `json:"model"`
	File           string   `json:"file"`            // base64-encoded audio
	Language       string   `json:"language,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
}

// TranscriptionWord is a word with timestamps.
type TranscriptionWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// TranscriptionSegment is a transcription segment.
type TranscriptionSegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}

// TranscriptionResponse is the response from a transcription request.
type TranscriptionResponse struct {
	Text     string                 `json:"text"`
	Task     string                 `json:"task"`
	Language string                 `json:"language"`
	Duration float64                `json:"duration"`
	Words    []TranscriptionWord    `json:"words"`
	Segments []TranscriptionSegment `json:"segments"`
}

// TranslationParams are the parameters for audio translation.
type TranslationParams struct {
	Model          string   `json:"model"`
	File           string   `json:"file"` // base64-encoded audio
	Prompt         string   `json:"prompt,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
}

// TranslationResponse is the response from a translation request.
type TranslationResponse struct {
	Text     string                 `json:"text"`
	Task     string                 `json:"task"`
	Language string                 `json:"language"`
	Duration float64                `json:"duration"`
	Segments []TranscriptionSegment `json:"segments"`
}

// ---------------------------------------------------------------------------
// Image types
// ---------------------------------------------------------------------------

// ImageGenerateParams are the parameters for image generation.
type ImageGenerateParams struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// ImageEditParams are the parameters for image editing.
type ImageEditParams struct {
	Model          string `json:"model"`
	Image          string `json:"image"` // base64
	Mask           string `json:"mask,omitempty"`
	Prompt         string `json:"prompt"`
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// ImageVariationParams are the parameters for creating image variations.
type ImageVariationParams struct {
	Model          string `json:"model"`
	Image          string `json:"image"` // base64
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// ImageData represents a single image in a response.
type ImageData struct {
	B64JSON       *string `json:"b64_json,omitempty"`
	URL           *string `json:"url,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
}

// ImageUsage contains image generation token usage.
type ImageUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ImageResponse is the response from an image request.
type ImageResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
	Model   string      `json:"model"`
	Usage   *ImageUsage `json:"usage,omitempty"`
}

// ---------------------------------------------------------------------------
// Moderation types
// ---------------------------------------------------------------------------

// ModerationParams are the parameters for a moderation request.
type ModerationParams struct {
	Model string `json:"model,omitempty"`
	Input any    `json:"input"` // string or []string
}

// ModerationCategories holds boolean flags for each category.
type ModerationCategories struct {
	Hate                  bool `json:"hate"`
	HateThreatening       bool `json:"hate_threatening"`
	Harassment            bool `json:"harassment"`
	HarassmentThreatening bool `json:"harassment_threatening"`
	Illicit               bool `json:"illicit"`
	IllicitViolent        bool `json:"illicit_violent"`
	SelfHarm              bool `json:"self_harm"`
	SelfHarmIntent        bool `json:"self_harm_intent"`
	SelfHarmInstructions  bool `json:"self_harm_instructions"`
	Sexual                bool `json:"sexual"`
	SexualMinors          bool `json:"sexual_minors"`
	Violence              bool `json:"violence"`
	ViolenceGraphic       bool `json:"violence_graphic"`
}

// ModerationCategoryScores holds float scores for each category.
type ModerationCategoryScores struct {
	Hate                  float64 `json:"hate"`
	HateThreatening       float64 `json:"hate_threatening"`
	Harassment            float64 `json:"harassment"`
	HarassmentThreatening float64 `json:"harassment_threatening"`
	Illicit               float64 `json:"illicit"`
	IllicitViolent        float64 `json:"illicit_violent"`
	SelfHarm              float64 `json:"self_harm"`
	SelfHarmIntent        float64 `json:"self_harm_intent"`
	SelfHarmInstructions  float64 `json:"self_harm_instructions"`
	Sexual                float64 `json:"sexual"`
	SexualMinors          float64 `json:"sexual_minors"`
	Violence              float64 `json:"violence"`
	ViolenceGraphic       float64 `json:"violence_graphic"`
}

// ModerationResult is a single moderation result.
type ModerationResult struct {
	Flagged        bool                     `json:"flagged"`
	Categories     ModerationCategories     `json:"categories"`
	CategoryScores ModerationCategoryScores `json:"category_scores"`
}

// ModerationResponse is the response from a moderation request.
type ModerationResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}

// ---------------------------------------------------------------------------
// Rerank types
// ---------------------------------------------------------------------------

// RerankParams are the parameters for a rerank request.
type RerankParams struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents,omitempty"`
	TopN      *int     `json:"top_n,omitempty"`
}

// RerankResult is a single rerank result.
type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       *string `json:"document,omitempty"`
}

// RerankMeta is metadata about the rerank response.
type RerankMeta struct {
	Version *string `json:"version,omitempty"`
}

// RerankResponse is the response from a rerank request.
type RerankResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Results []RerankResult `json:"results"`
	Meta    *RerankMeta    `json:"meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Responses API types
// ---------------------------------------------------------------------------

// ResponseCreateParams are the parameters for creating a response.
type ResponseCreateParams struct {
	Model             string         `json:"model"`
	Input             []any          `json:"input"`
	Instructions      string         `json:"instructions,omitempty"`
	Tools             []any          `json:"tools,omitempty"`
	BuiltinTools      []any          `json:"builtin_tools,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	Reasoning         map[string]any `json:"reasoning,omitempty"`
	MaxOutputTokens   *int           `json:"max_output_tokens,omitempty"`
	Temperature       *float64       `json:"temperature,omitempty"`
	TopP              *float64       `json:"top_p,omitempty"`
	Store             *bool          `json:"store,omitempty"`
	PreviousResponseID string       `json:"previous_response_id,omitempty"`
	Stream            bool           `json:"stream,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// ResponseListParams are the parameters for listing responses.
type ResponseListParams struct {
	Status string `json:"status,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	Order  string `json:"order,omitempty"`
}

// ResponseOutputItem is an item in a response output.
type ResponseOutputItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Role      string `json:"role,omitempty"`
	Content   []any  `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

// ResponseUsage contains response token usage.
type ResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponseObject is the response from the Responses API.
type ResponseObject struct {
	ID                 string               `json:"id"`
	Object             string               `json:"object"`
	CreatedAt          int64                `json:"created_at"`
	Status             string               `json:"status"`
	Model              string               `json:"model"`
	Output             []ResponseOutputItem `json:"output"`
	Usage              *ResponseUsage       `json:"usage,omitempty"`
	Metadata           map[string]string    `json:"metadata"`
	Temperature        float64              `json:"temperature"`
	TopP               float64              `json:"top_p"`
	MaxOutputTokens    int                  `json:"max_output_tokens"`
	PreviousResponseID *string              `json:"previous_response_id,omitempty"`
}

// DeleteResponseResult is the result of deleting a response.
type DeleteResponseResult struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

// ListResponsesResult is the result of listing responses.
type ListResponsesResult struct {
	Data    []ResponseObject `json:"data"`
	FirstID string           `json:"first_id"`
	LastID  string           `json:"last_id"`
	HasMore bool             `json:"has_more"`
}
