package gateway

import (
	"encoding/json"
	"time"
)

// MessageRole represents who authored a message in a conversation.
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleFunction  MessageRole = "function"
	RoleTool      MessageRole = "tool"
)

// ContentPart represents a single piece of content within a message.
// Types can be: "text", "image_url", etc.
type ContentPart struct {
	Type         string  `json:"type"`
	Text         *string `json:"text,omitempty"`
	ImageURL     *string `json:"image_url,omitempty"`
	FileID       *string `json:"file_id,omitempty"`
	ToolCallID   *string `json:"tool_call_id,omitempty"`
	FunctionCall *string `json:"function_call,omitempty"`
	// ProviderJSON preserves a provider-native content chunk for replay while
	// normalized consumers continue to use Type/Text for display.
	ProviderJSON *json.RawMessage `json:"provider_json,omitempty"`
}

// ToolCall represents a tool call from the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the function name and arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Message represents a chat message with potentially multi-part content (text, images, etc.).
type Message struct {
	Role       MessageRole   `json:"role"`
	Content    []ContentPart `json:"content"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`   // Tool calls from assistant
	ToolCallID string        `json:"tool_call_id,omitempty"` // For tool response messages
}

// SamplingParams configures how models should sample tokens.
type SamplingParams struct {
	Temperature           float64  `json:"temperature,omitempty"`
	TemperatureConfigured bool     `json:"-"`
	TopP                  float64  `json:"top_p,omitempty"`
	TopPConfigured        bool     `json:"-"`
	MaxTokens             int      `json:"max_tokens,omitempty"`
	MaxCompletionTokens   int      `json:"max_completion_tokens,omitempty"`
	Stop                  []string `json:"stop,omitempty"`
	FrequencyPenalty      float64  `json:"frequency_penalty,omitempty"`
	FrequencyConfigured   bool     `json:"-"`
	PresencePenalty       float64  `json:"presence_penalty,omitempty"`
	PresenceConfigured    bool     `json:"-"`
	ReasoningEffort       string   `json:"reasoning_effort,omitempty"`
	ReasoningBudget       *int     `json:"reasoning_budget_tokens,omitempty"`
	ReasoningEnabled      *bool    `json:"reasoning_enabled,omitempty"`
	// TopK and Seed are pointers because zero is a meaningful value the
	// provider must be told about, not an absent setting.
	TopK *int   `json:"top_k,omitempty"`
	Seed *int64 `json:"seed,omitempty"`
	// Verbosity is GPT-5's output-length hint: "low", "medium" or "high".
	Verbosity string `json:"verbosity,omitempty"`
}

// TokenDetails provides detailed breakdown of token usage.
// Different providers expose different subsets of these fields.
//
// Cache accounting follows the opencode "dual-track" pattern: the
// inclusive Usage.PromptTokens covers cached + fresh, while the
// per-bucket fields here are non-overlapping. The invariant is:
//
//	PromptTokens = (fresh, non-cached) + CacheReadTokens + CacheWriteTokens
//
// Consumers compute the fresh portion as
// PromptTokens − CacheReadTokens − CacheWriteTokens — that subtraction
// is the right shape for billing because each bucket has its own rate
// (Anthropic: ~10% cache read, ~125% cache write, 100% fresh).
//
// Surfaces that care about *context-window occupancy* use the
// inclusive PromptTokens. Surfaces that care about *billable cost*
// read the buckets and apply per-bucket pricing.
type TokenDetails struct {
	CachedTokens     int `json:"cached_tokens,omitempty"`      // CacheReadTokens + CacheWriteTokens (legacy aggregate; kept for back-compat)
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`  // Cache hits — re-using a prior request's cached prefix
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"` // Cache writes — tokens written into the cache this call (Anthropic only)
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`   // o1/o3 reasoning models
	AudioTokens      int `json:"audio_tokens,omitempty"`       // Multimodal audio input/output
	TextTokens       int `json:"text_tokens,omitempty"`        // Text tokens (when separated from audio); not used for cache split
	ImageTokens      int `json:"image_tokens,omitempty"`       // Vision model image tokens
}

// Usage describes token usage for a request.
type Usage struct {
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	KeySource        string `json:"-"`
	// Detailed breakdown (optional, provider-specific)
	PromptDetails     *TokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionDetails *TokenDetails `json:"completion_tokens_details,omitempty"`
}

// CacheReadCount reports how many prompt tokens were served from the provider's
// cache, or zero when the provider does not report caching.
//
// Cost calculation needs this: cache reads are priced far below fresh input
// (a tenth of the input rate at most providers, a fiftieth at DeepSeek), so a
// caller that passes zero here bills every cached token at the full input rate.
func (u *Usage) CacheReadCount() int {
	if u == nil || u.PromptDetails == nil {
		return 0
	}
	// Prefer the explicit read count; fall back to the legacy aggregate for
	// providers still reporting only CachedTokens.
	if u.PromptDetails.CacheReadTokens > 0 {
		return u.PromptDetails.CacheReadTokens
	}
	return u.PromptDetails.CachedTokens
}

// Choice represents a single non-streaming choice from the model.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// ChoiceDelta represents a streaming delta for a single choice index.
type ChoiceDelta struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// ToolDefinition defines a tool that the model can use.
type ToolDefinition struct {
	Type     string          `json:"type"` // "function"
	Function ToolFunctionDef `json:"function"`
}

// ToolFunctionDef defines a function that can be called.
type ToolFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"` // JSON Schema
}

// ChatCompletionRequest is the normalized chat request shape across providers.
type ChatCompletionRequest struct {
	Model           string                 `json:"model"`
	Messages        []Message              `json:"messages"`
	Tools           []ToolDefinition       `json:"tools,omitempty"`       // Available tools
	ToolChoice      interface{}            `json:"tool_choice,omitempty"` // "auto", "none", or specific tool
	Sampling        SamplingParams         `json:"sampling,omitempty"`
	Stream          bool                   `json:"stream,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	UseResponsesAPI bool                   `json:"-"` // When true, provider uses /v1/responses instead of /v1/chat/completions
}

// ChatCompletionResponse is the normalized non-streaming response shape.
type ChatCompletionResponse struct {
	ID        string    `json:"id"`
	Created   time.Time `json:"created"`
	Model     string    `json:"model"`
	Choices   []Choice  `json:"choices"`
	Usage     Usage     `json:"usage"`
	KeySource string    `json:"-"`
}

// ChatResponseChunk is the normalized streaming chunk shape.
type ChatResponseChunk struct {
	ID      string        `json:"id"`
	Created time.Time     `json:"created"`
	Model   string        `json:"model"`
	Choices []ChoiceDelta `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"` // Some providers send usage in the final chunk
}

// EmbeddingsRequest represents a single embedding generation request.
type EmbeddingsRequest struct {
	Model    string                 `json:"model"`
	Input    string                 `json:"input"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// EmbeddingsResponse is a normalized embedding output.
type EmbeddingsResponse struct {
	Embedding []float64 `json:"embedding"`
	Model     string    `json:"model,omitempty"`
	Usage     *Usage    `json:"usage,omitempty"` // Token usage for billing
}

// =============================================================================
// Audio API Types (TTS + STT)
// =============================================================================

// SpeechRequest represents a text-to-speech request.
type SpeechRequest struct {
	Model          string                 `json:"model"`
	Input          string                 `json:"input"`
	Voice          string                 `json:"voice"`
	ResponseFormat string                 `json:"response_format,omitempty"` // mp3, opus, aac, flac, wav, pcm
	Speed          float64                `json:"speed,omitempty"`           // 0.25 to 4.0
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	// Voice cloning fields
	ReferenceAudio      []byte `json:"reference_audio,omitempty"`        // Raw audio bytes for voice cloning
	ReferenceText       string `json:"reference_text,omitempty"`         // Transcript of reference audio
	VoiceCloneProfileID string `json:"voice_clone_profile_id,omitempty"` // Reuse a previously cloned voice
	// Audio generation parameters
	Instructions string  `json:"instructions,omitempty"` // Natural language style instructions (Qwen instruct models)
	Temperature  float64 `json:"temperature,omitempty"`  // Sampling temperature (provider-specific)
	TopP         float64 `json:"top_p,omitempty"`        // Top-p sampling (provider-specific)
	Stability    float64 `json:"stability,omitempty"`    // Voice stability (ElevenLabs-style, 0-1)
	Similarity   float64 `json:"similarity,omitempty"`   // Voice similarity boost (ElevenLabs-style, 0-1)
	Style        float64 `json:"style,omitempty"`        // Style exaggeration (ElevenLabs-style, 0-1)
	// Audio post-processing
	Enhancement  bool    `json:"enhancement,omitempty"`   // Enable audio enhancement (normalize + noise gate)
	SpeakerBoost float64 `json:"speaker_boost,omitempty"` // Speaker volume boost (0-1, 0=off)
}

// SpeechResponse represents generated audio.
type SpeechResponse struct {
	Audio           []byte  `json:"audio"`
	Format          string  `json:"format"`
	ContentType     string  `json:"content_type"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	InputCharacters int     `json:"input_characters,omitempty"`
}

// TranscriptionRequest represents a speech-to-text transcription request.
type TranscriptionRequest struct {
	File                   []byte                 `json:"file"`
	Model                  string                 `json:"model"`
	Language               string                 `json:"language,omitempty"`
	Prompt                 string                 `json:"prompt,omitempty"`
	ResponseFormat         string                 `json:"response_format,omitempty"` // json, text, srt, verbose_json, vtt
	Temperature            float64                `json:"temperature,omitempty"`
	TimestampGranularities []string               `json:"timestamp_granularities,omitempty"` // word, segment
	Filename               string                 `json:"filename,omitempty"`
	Metadata               map[string]interface{} `json:"metadata,omitempty"`
}

// TranscriptionWord represents word-level timing.
type TranscriptionWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// TranscriptionSegment represents segment-level timing.
type TranscriptionSegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens,omitempty"`
	Temperature      float64 `json:"temperature,omitempty"`
	AvgLogprob       float64 `json:"avg_logprob,omitempty"`
	CompressionRatio float64 `json:"compression_ratio,omitempty"`
	NoSpeechProb     float64 `json:"no_speech_prob,omitempty"`
}

// TranscriptionResponse represents the transcription result.
type TranscriptionResponse struct {
	Text     string                 `json:"text"`
	Task     string                 `json:"task,omitempty"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	Words    []TranscriptionWord    `json:"words,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
}

// TranslationRequest represents an audio translation request.
type TranslationRequest struct {
	File           []byte                 `json:"file"`
	Model          string                 `json:"model"`
	Prompt         string                 `json:"prompt,omitempty"`
	ResponseFormat string                 `json:"response_format,omitempty"`
	Temperature    float64                `json:"temperature,omitempty"`
	Filename       string                 `json:"filename,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// TranslationResponse represents the translation result.
type TranslationResponse struct {
	Text     string                 `json:"text"`
	Task     string                 `json:"task,omitempty"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
}

// =============================================================================
// Images API Types
// =============================================================================

// ImageGenerationRequest represents an image generation request.
type ImageGenerationRequest struct {
	Prompt         string                 `json:"prompt"`
	Model          string                 `json:"model,omitempty"`
	N              int                    `json:"n,omitempty"`
	Quality        string                 `json:"quality,omitempty"`
	ResponseFormat string                 `json:"response_format,omitempty"` // url or b64_json
	Size           string                 `json:"size,omitempty"`
	Style          string                 `json:"style,omitempty"`
	User           string                 `json:"user,omitempty"`
	Background     string                 `json:"background,omitempty"`
	OutputFormat   string                 `json:"output_format,omitempty"`
	Moderation     string                 `json:"moderation,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ImageData represents a single generated image.
type ImageData struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ImageUsage represents token usage for image generation.
type ImageUsage struct {
	InputTokens         int                `json:"input_tokens,omitempty"`
	OutputTokens        int                `json:"output_tokens,omitempty"`
	TotalTokens         int                `json:"total_tokens,omitempty"`
	InputTokensDetails  *ImageTokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *ImageTokenDetails `json:"output_tokens_details,omitempty"`
}

// ImageTokenDetails represents detailed token breakdown for images.
type ImageTokenDetails struct {
	TextTokens  int `json:"text_tokens,omitempty"`
	ImageTokens int `json:"image_tokens,omitempty"`
}

// ImageGenerationResponse represents the image generation result.
type ImageGenerationResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
	Model   string      `json:"model,omitempty"`
	Usage   *ImageUsage `json:"usage,omitempty"`
}

// ImageEditRequest represents an image edit request.
type ImageEditRequest struct {
	Image          []byte                 `json:"image"`
	Prompt         string                 `json:"prompt"`
	Mask           []byte                 `json:"mask,omitempty"`
	Model          string                 `json:"model,omitempty"`
	N              int                    `json:"n,omitempty"`
	Size           string                 `json:"size,omitempty"`
	ResponseFormat string                 `json:"response_format,omitempty"`
	User           string                 `json:"user,omitempty"`
	Quality        string                 `json:"quality,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ImageEditResponse represents the image edit result.
type ImageEditResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
	Model   string      `json:"model,omitempty"`
	Usage   *ImageUsage `json:"usage,omitempty"`
}

// ImageVariationRequest represents an image variation request.
type ImageVariationRequest struct {
	Image          []byte                 `json:"image"`
	Model          string                 `json:"model,omitempty"`
	N              int                    `json:"n,omitempty"`
	ResponseFormat string                 `json:"response_format,omitempty"`
	Size           string                 `json:"size,omitempty"`
	User           string                 `json:"user,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ImageVariationResponse represents the image variation result.
type ImageVariationResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
	Model   string      `json:"model,omitempty"`
}

// =============================================================================
// Moderations API Types
// =============================================================================

// ModerationInput represents content for moderation.
type ModerationInput struct {
	Type     string              `json:"type"` // "text" or "image_url"
	Text     string              `json:"text,omitempty"`
	ImageURL *ModerationImageURL `json:"image_url,omitempty"`
}

// ModerationImageURL represents an image URL for moderation.
type ModerationImageURL struct {
	URL string `json:"url"`
}

// ModerationRequest represents a moderation request.
type ModerationRequest struct {
	Input    string                 `json:"input,omitempty"`
	Inputs   []ModerationInput      `json:"inputs,omitempty"`
	Model    string                 `json:"model,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ModerationCategories represents category flags.
type ModerationCategories struct {
	Hate                  bool `json:"hate"`
	HateThreatening       bool `json:"hate/threatening"`
	Harassment            bool `json:"harassment"`
	HarassmentThreatening bool `json:"harassment/threatening"`
	Illicit               bool `json:"illicit"`
	IllicitViolent        bool `json:"illicit/violent"`
	SelfHarm              bool `json:"self-harm"`
	SelfHarmIntent        bool `json:"self-harm/intent"`
	SelfHarmInstructions  bool `json:"self-harm/instructions"`
	Sexual                bool `json:"sexual"`
	SexualMinors          bool `json:"sexual/minors"`
	Violence              bool `json:"violence"`
	ViolenceGraphic       bool `json:"violence/graphic"`
}

// ModerationCategoryScores represents category confidence scores.
type ModerationCategoryScores struct {
	Hate                  float64 `json:"hate"`
	HateThreatening       float64 `json:"hate/threatening"`
	Harassment            float64 `json:"harassment"`
	HarassmentThreatening float64 `json:"harassment/threatening"`
	Illicit               float64 `json:"illicit"`
	IllicitViolent        float64 `json:"illicit/violent"`
	SelfHarm              float64 `json:"self-harm"`
	SelfHarmIntent        float64 `json:"self-harm/intent"`
	SelfHarmInstructions  float64 `json:"self-harm/instructions"`
	Sexual                float64 `json:"sexual"`
	SexualMinors          float64 `json:"sexual/minors"`
	Violence              float64 `json:"violence"`
	ViolenceGraphic       float64 `json:"violence/graphic"`
}

// ModerationResult represents a single moderation result.
type ModerationResult struct {
	Flagged        bool                     `json:"flagged"`
	Categories     ModerationCategories     `json:"categories"`
	CategoryScores ModerationCategoryScores `json:"category_scores"`
}

// ModerationResponse represents the moderation response.
type ModerationResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}

// =============================================================================
// Rerank API Types
// =============================================================================

// RerankDocument represents a document for reranking.
type RerankDocument struct {
	Text string `json:"text"`
}

// RerankRequest represents a rerank request.
type RerankRequest struct {
	Model           string                 `json:"model"`
	Query           string                 `json:"query"`
	Documents       []string               `json:"documents,omitempty"`
	DocumentObjects []RerankDocument       `json:"document_objects,omitempty"`
	TopN            int                    `json:"top_n,omitempty"`
	ReturnDocuments bool                   `json:"return_documents,omitempty"`
	MaxTokensPerDoc int                    `json:"max_tokens_per_doc,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// RerankResult represents a single rerank result.
type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       string  `json:"document,omitempty"`
}

// RerankBilledUnits represents billing information.
type RerankBilledUnits struct {
	SearchUnits  int `json:"search_units,omitempty"`
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// RerankTokens represents token usage.
type RerankTokens struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// RerankMeta represents API metadata.
type RerankMeta struct {
	Version     string             `json:"version,omitempty"`
	IsBillable  bool               `json:"is_billable,omitempty"`
	BilledUnits *RerankBilledUnits `json:"billed_units,omitempty"`
	Tokens      *RerankTokens      `json:"tokens,omitempty"`
}

// RerankResponse represents the rerank response.
type RerankResponse struct {
	ID      string         `json:"id,omitempty"`
	Model   string         `json:"model,omitempty"`
	Results []RerankResult `json:"results"`
	Meta    *RerankMeta    `json:"meta,omitempty"`
}

// =============================================================================
// Responses API Types (Agentic)
// =============================================================================

// ResponseInput represents input for the Responses API.
type ResponseInput struct {
	Type    string        `json:"type"` // "message", "item_reference"
	Role    MessageRole   `json:"role,omitempty"`
	Content []ContentPart `json:"content,omitempty"`
	ItemID  string        `json:"item_id,omitempty"`
}

// BuiltInToolConfig represents configuration for built-in tools.
type BuiltInToolConfig struct {
	Type            string                 `json:"type"` // web_search, file_search, code_interpreter, computer_use, mcp
	WebSearch       *WebSearchConfig       `json:"web_search,omitempty"`
	FileSearch      *FileSearchConfig      `json:"file_search,omitempty"`
	CodeInterpreter *CodeInterpreterConfig `json:"code_interpreter,omitempty"`
	ComputerUse     *ComputerUseConfig     `json:"computer_use,omitempty"`
	MCP             *McpServerConfig       `json:"mcp,omitempty"`
}

// WebSearchConfig configures web search.
type WebSearchConfig struct {
	MaxResults        int                `json:"max_results,omitempty"`
	SearchContextSize string             `json:"search_context_size,omitempty"`
	UserLocation      *WebSearchLocation `json:"user_location,omitempty"`
}

// WebSearchLocation represents user location for localized search.
type WebSearchLocation struct {
	Type     string `json:"type,omitempty"`
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Country  string `json:"country,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// FileSearchConfig configures file search.
type FileSearchConfig struct {
	VectorStoreIDs []string                  `json:"vector_store_ids,omitempty"`
	MaxNumResults  int                       `json:"max_num_results,omitempty"`
	RankingOptions *FileSearchRankingOptions `json:"ranking_options,omitempty"`
}

// FileSearchRankingOptions configures file search ranking.
type FileSearchRankingOptions struct {
	Ranker         string  `json:"ranker,omitempty"`
	ScoreThreshold float64 `json:"score_threshold,omitempty"`
}

// CodeInterpreterConfig configures code interpreter.
type CodeInterpreterConfig struct {
	Container *CodeInterpreterContainer `json:"container,omitempty"`
}

// CodeInterpreterContainer configures the code interpreter container.
type CodeInterpreterContainer struct {
	Type    string   `json:"type,omitempty"`
	FileIDs []string `json:"file_ids,omitempty"`
}

// ComputerUseConfig configures computer use.
type ComputerUseConfig struct {
	Environment   string `json:"environment,omitempty"`
	DisplayWidth  int    `json:"display_width,omitempty"`
	DisplayHeight int    `json:"display_height,omitempty"`
}

// McpServerConfig configures MCP server connection.
type McpServerConfig struct {
	URL             string            `json:"url"`
	Name            string            `json:"name,omitempty"`
	ToolFilter      *McpToolFilter    `json:"tool_filter,omitempty"`
	RequireApproval bool              `json:"require_approval,omitempty"`
	AllowedTools    []string          `json:"allowed_tools,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
}

// McpToolFilter configures MCP tool filtering.
type McpToolFilter struct {
	Type  string   `json:"type,omitempty"` // include, exclude
	Tools []string `json:"tools,omitempty"`
}

// ResponseTextConfig configures text output.
type ResponseTextConfig struct {
	Format *ResponseFormatConfig `json:"format,omitempty"`
}

// ResponseFormatConfig configures response format.
type ResponseFormatConfig struct {
	Type       string                 `json:"type,omitempty"` // text, json_schema, json_object
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

// ReasoningConfig configures reasoning for advanced models.
type ReasoningConfig struct {
	Effort          string `json:"effort,omitempty"`           // low, medium, high
	GenerateSummary string `json:"generate_summary,omitempty"` // concise, summary
}

// TruncationStrategy configures context truncation.
type TruncationStrategy struct {
	Type         string `json:"type,omitempty"` // auto, disabled
	LastMessages int    `json:"last_messages,omitempty"`
}

// CreateResponseRequest represents a Responses API request.
type CreateResponseRequest struct {
	Model             string              `json:"model"`
	Instructions      string              `json:"instructions,omitempty"`
	Input             []ResponseInput     `json:"input,omitempty"`
	Tools             []ToolDefinition    `json:"tools,omitempty"`
	BuiltinTools      []BuiltInToolConfig `json:"builtin_tools,omitempty"`
	ToolChoice        interface{}         `json:"tool_choice,omitempty"`
	ParallelToolCalls bool                `json:"parallel_tool_calls,omitempty"`
	Text              *ResponseTextConfig `json:"text,omitempty"`
	Reasoning         *ReasoningConfig    `json:"reasoning,omitempty"`
	MaxOutputTokens   int                 `json:"max_output_tokens,omitempty"`
	// *Configured track whether the caller set the value explicitly, so a
	// model-scoped variant can supply a default without overriding an
	// explicit zero.
	MaxOutputConfigured   bool                `json:"-"`
	Temperature           float64             `json:"temperature,omitempty"`
	TemperatureConfigured bool                `json:"-"`
	TopP                  float64             `json:"top_p,omitempty"`
	TopPConfigured        bool                `json:"-"`
	Truncation            *TruncationStrategy `json:"truncation,omitempty"`
	Store                 bool                `json:"store,omitempty"`
	PreviousResponseID    string              `json:"previous_response_id,omitempty"`
	Stream                bool                `json:"stream,omitempty"`
	Metadata              map[string]string   `json:"metadata,omitempty"`
}

// ResponseOutputItem represents an output item from a response.
type ResponseOutputItem struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"` // message, function_call, function_call_output, web_search_call, etc.
	Status     string             `json:"status,omitempty"`
	Role       MessageRole        `json:"role,omitempty"`
	Content    []ContentPart      `json:"content,omitempty"`
	CallID     string             `json:"call_id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Arguments  string             `json:"arguments,omitempty"`
	Output     string             `json:"output,omitempty"`
	WebSearch  *WebSearchResults  `json:"web_search_results,omitempty"`
	FileSearch *FileSearchResults `json:"file_search_results,omitempty"`
}

// WebSearchResults represents web search results.
type WebSearchResults struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results"`
}

// WebSearchResult represents a single web search result.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// FileSearchResults represents file search results.
type FileSearchResults struct {
	Query   string             `json:"query"`
	Results []FileSearchResult `json:"results"`
}

// FileSearchResult represents a single file search result.
type FileSearchResult struct {
	FileID   string  `json:"file_id"`
	FileName string  `json:"file_name"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
}

// ResponseUsage represents token usage for responses.
type ResponseUsage struct {
	InputTokens         int                   `json:"input_tokens"`
	OutputTokens        int                   `json:"output_tokens"`
	TotalTokens         int                   `json:"total_tokens"`
	InputTokensDetails  *ResponseUsageDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *ResponseUsageDetails `json:"output_tokens_details,omitempty"`
}

// ResponseUsageDetails represents detailed token breakdown.
type ResponseUsageDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	TextTokens      int `json:"text_tokens,omitempty"`
	AudioTokens     int `json:"audio_tokens,omitempty"`
	ImageTokens     int `json:"image_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// IncompleteDetails represents why a response is incomplete.
type IncompleteDetails struct {
	Reason string `json:"reason"`
}

// ResponseError represents an error in the response.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CreateResponseResponse represents the Responses API response.
type CreateResponseResponse struct {
	ID                 string               `json:"id"`
	Object             string               `json:"object"`
	CreatedAt          int64                `json:"created_at"`
	Status             string               `json:"status"` // in_progress, completed, incomplete, failed, cancelled
	Error              *ResponseError       `json:"error,omitempty"`
	IncompleteDetails  *IncompleteDetails   `json:"incomplete_details,omitempty"`
	Model              string               `json:"model"`
	Output             []ResponseOutputItem `json:"output"`
	Usage              *ResponseUsage       `json:"usage,omitempty"`
	Metadata           map[string]string    `json:"metadata,omitempty"`
	Temperature        float64              `json:"temperature,omitempty"`
	TopP               float64              `json:"top_p,omitempty"`
	MaxOutputTokens    int                  `json:"max_output_tokens,omitempty"`
	PreviousResponseID string               `json:"previous_response_id,omitempty"`
	Reasoning          *ReasoningConfig     `json:"reasoning,omitempty"`
	Truncation         *TruncationStrategy  `json:"truncation,omitempty"`
}
