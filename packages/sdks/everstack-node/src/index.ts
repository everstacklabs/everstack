/**
 * @everstack/everstack - Everstack Node.js SDK
 *
 * OpenAI-style SDK for the Everstack AI Gateway with type-safe model selection.
 *
 * @example
 * ```typescript
 * import Everstack from '@everstack/everstack';
 *
 * const client = new Everstack({ apiKey: 'pk_...' });
 *
 * const response = await client.chat.completions.create({
 *   model: '@openai/gpt-4o',
 *   messages: [{ role: 'user', content: 'Hello!' }],
 * });
 *
 * console.log(response.choices[0].message.content);
 * ```
 *
 * @packageDocumentation
 */

// Main client
export { Everstack, type EverstackOptions } from "./client.js";
export { Everstack as default } from "./client.js";

// OpenAI SDK compatibility
export {
  createHeaders,
  createOpenAIConfig,
  EVERSTACK_GATEWAY_URL,
  type HeaderOptions,
} from "./compat.js";

// Errors
export {
  APIError,
  AuthenticationError,
  ConnectionError,
  InternalServerError,
  InvalidModelError,
  NotFoundError,
  PermissionDeniedError,
  EverstackError,
  RateLimitError,
  ServiceUnavailableError,
  TimeoutError,
} from "./errors.js";

// Types
export type {
  // Common
  ContentPart,
  FinishReason,
  FunctionDefinition,
  MessageContent,
  ResponseFormat,
  ResponseFormatType,
  Role,
  Tool,
  ToolCall,
  ToolChoice,
  Usage,
  // Chat
  AssistantMessage,
  ChatCompletionChoice,
  ChatCompletionChunk,
  ChatCompletionChunkChoice,
  ChatCompletionDelta,
  ChatCompletionParams,
  ChatCompletionResponse,
  ChatMessage,
  FallbackInfo,
  Message,
  SystemMessage,
  ToolMessage,
  UserMessage,
  // Embeddings
  EmbeddingData,
  EmbeddingEncodingFormat,
  EmbeddingInput,
  EmbeddingsParams,
  EmbeddingsResponse,
  // Models
  AllModels,
  AnthropicModel,
  CohereModel,
  DeepseekModel,
  GoogleModel,
  HuggingfaceModel,
  MinimaxModel,
  MistralModel,
  Model,
  ModelMetadata,
  ModelsListResponse,
  MoonshotModel,
  OpenaiModel,
  OpenrouterModel,
  QwenModel,
  Provider,
  // Memory
  AddDocumentsParams,
  AddDocumentsResponse,
  AnalyticsBucket,
  AnalyticsSummary,
  CollectionStat,
  CreateCollectionParams,
  CreateCollectionResponse,
  DeleteCollectionResponse,
  DeleteDocumentResponse,
  DocumentInput,
  ListCollectionsResponse,
  MemoryCollection,
  QueryParams,
  QueryResponse,
  SearchResult,
} from "./types/index.js";

// Agent types (ergonomic)
export type {
  Agent,
  AgentIdentityParams,
  AgentMode,
  AgentStreamEvent,
  CreateAgentParams,
  CreateSandboxParams,
  CreateSandboxResult,
  CreateSessionParams,
  CreateTriggerParams,
  DatabaseConfig,
  DeployAgentParams,
  ErrorEvent,
  ExecutionPolicy,
  ExposePortParams,
  FallbackEvent,
  GenericEvent,
  LifecycleMode,
  LifecycleStatus,
  ListAgentsParams,
  MemoryConfig,
  ReviewPendingEvent,
  ReviewResolvedEvent,
  RunTurnParams,
  RunTurnResult,
  RunTurnStreamParams,
  SandboxConfig,
  SandboxExecution,
  SandboxExecEvent,
  SandboxInstance,
  SandboxStats,
  SandboxStatusLiteral,
  Session,
  SessionStatus,
  SubmitReviewParams,
  TaskPermission,
  TextDeltaEvent,
  ToolCallEvent,
  ToolResultEvent,
  Turn,
  TurnEndEvent,
  TurnStartEvent,
  TurnStatus,
  UpdateAgentParams,
  UserInputRequestEvent,
  WorkersConfig,
} from "./types/index.js";

// Agent / dataset / evaluation / observability / scores / channels proto types (raw)
export type * from "@everstack/proto/everstack/agents/v1/agents_pb.js";
export type * from "@everstack/proto/everstack/channels/v1/channels_pb.js";
export type * from "@everstack/proto/everstack/datasets/v1/datasets_pb.js";
export type * from "@everstack/proto/everstack/scores/v1/service_pb.js";
export type * from "@everstack/proto/everstack/traces/v1/observability_pb.js";

// Model utilities
export {
  allModels,
  getModelMetadata,
  getModelsByProvider,
  isValidModel,
  modelMetadata,
  parseModelId,
  providers,
} from "./types/index.js";

// Resources (for advanced usage)
export { Chat, Completions } from "./resources/chat.js";
export { Agents, AgentStream } from "./resources/agents.js";
export { Channels } from "./resources/channels.js";
export { Datasets, Evaluations } from "./resources/datasets.js";
export { Embeddings } from "./resources/embeddings.js";
export { Memory, Collections } from "./resources/memory.js";
export { Models } from "./resources/models.js";
export { Audio, Speech, Transcriptions, Translations } from "./resources/audio.js";
export { Images } from "./resources/images.js";
export { Identity, type IdentityInfo } from "./resources/identity.js";
export { Moderations } from "./resources/moderations.js";
export { Rerank } from "./resources/rerank.js";
export { Responses } from "./resources/responses.js";
export { Observability } from "./resources/observability.js";
export { Scores } from "./resources/scores.js";
export { Traces, TraceSpan } from "./resources/traces.js";
export type { TraceSpanOptions, TraceEmitConfig } from "./resources/traces.js";
export { OtelSpan } from "./tracing/otel-span.js";
export type {
  OtelSpanOptions,
  ObservationType,
  SpanKind,
} from "./tracing/otel-span.js";

// New resource types
export type {
  SpeechParams,
  SpeechResponse,
  TranscriptionParams,
  TranscriptionResponse,
  TranslationParams,
  TranslationResponse,
} from "./resources/audio.js";
export type {
  ImageGenerateParams,
  ImageEditParams,
  ImageVariationParams,
  ImageData,
  ImageResponse,
  ImageUsage,
} from "./resources/images.js";
export type {
  ModerationParams,
  ModerationCategories,
  ModerationCategoryScores,
  ModerationResult,
  ModerationResponse,
} from "./resources/moderations.js";
export type {
  RerankParams,
  RerankResult,
  RerankMeta,
  RerankResponse,
} from "./resources/rerank.js";
export type {
  CreateResponseParams,
  ResponseInput,
  ResponseObject,
  ResponseOutputItem,
  ResponseUsage,
  ResponseStreamEvent,
  DeleteResponseResult,
  ListResponsesParams,
  ListResponsesResult,
} from "./resources/responses.js";
