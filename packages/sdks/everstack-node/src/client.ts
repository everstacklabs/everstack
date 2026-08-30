/**
 * Everstack SDK Client
 *
 * The main entry point for the Everstack SDK. Provides OpenAI-style API
 * with type-safe model selection.
 *
 * @example
 * ```typescript
 * import Everstack from '@everstack/everstack';
 *
 * // Option 1: Synchronous constructor (uses full catalog types)
 * const client = new Everstack({ apiKey: 'pk_...' });
 *
 * // Option 2: Async init (fetches user's available models)
 * const client = await Everstack.init({ apiKey: 'pk_...' });
 *
 * const response = await client.chat.completions.create({
 *   model: '@openai/gpt-4o',
 *   messages: [{ role: 'user', content: 'Hello!' }],
 * });
 * ```
 */

import { createClient, type Client } from "@connectrpc/connect";
import { AgentsService } from "@everstack/proto/everstack/agents/v1/agents_service_pb.js";
import { ChannelsService } from "@everstack/proto/everstack/channels/v1/channels_service_pb.js";
import { CLIService } from "@everstack/proto/everstack/cli/v1/cli_service_pb.js";
import {
  DatasetService,
  EvalService,
} from "@everstack/proto/everstack/datasets/v1/datasets_service_pb.js";
import { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { ScoreService } from "@everstack/proto/everstack/scores/v1/service_pb.js";
import { ObservabilityService } from "@everstack/proto/everstack/traces/v1/observability_service_pb.js";
import { TracesService } from "@everstack/proto/everstack/traces/v1/traces_service_pb.js";

import { Agents } from "./resources/agents.js";
import { Audio } from "./resources/audio.js";
import { Channels } from "./resources/channels.js";
import { Chat } from "./resources/chat.js";
import { Datasets, Evaluations } from "./resources/datasets.js";
import { Embeddings } from "./resources/embeddings.js";
import { Images } from "./resources/images.js";
import { Identity } from "./resources/identity.js";
import { Memory } from "./resources/memory.js";
import { Models } from "./resources/models.js";
import { Moderations } from "./resources/moderations.js";
import { Observability } from "./resources/observability.js";
import { Rerank } from "./resources/rerank.js";
import { Responses } from "./resources/responses.js";
import { Scores } from "./resources/scores.js";
import { Traces, type CaptureOptions } from "./resources/traces.js";
import { createTransport, type TransportOptions } from "./transport.js";
import type { AllModels } from "./generated/models.js";
import { EVERSTACK_GATEWAY_URL } from "./compat.js";

/**
 * Options for creating a Everstack client
 */
export interface EverstackOptions {
  /** Everstack API key (required) */
  apiKey: string;

  /** Base URL of the Everstack gateway */
  baseUrl?: string;

  /** Default provider for routing (e.g., "@openai") */
  provider?: string;

  /** Organization ID for multi-tenant setups */
  orgId?: string;

  /** User ID for tracking/attribution */
  userId?: string;

  /** Additional default headers */
  headers?: Record<string, string>;

  /** Request timeout in milliseconds */
  timeout?: number;

  /** Maximum number of retries */
  maxRetries?: number;
}

/**
 * Everstack client class
 *
 * @typeParam TModels - The type of models available to this client.
 *   Defaults to AllModels (full catalog). When using `Everstack.init()`,
 *   this is narrowed to the user's available models.
 */
export class Everstack<TModels extends string = AllModels> {
  /**
   * Chat completions resource
   *
   * @example
   * ```typescript
   * const response = await client.chat.completions.create({
   *   model: '@openai/gpt-4o',
   *   messages: [{ role: 'user', content: 'Hello!' }],
   * });
   * ```
   */
  readonly chat: Chat<TModels>;

  /**
   * Embeddings resource
   *
   * @example
   * ```typescript
   * const response = await client.embeddings.create({
   *   model: '@openai/text-embedding-3-small',
   *   input: 'Hello, world!',
   * });
   * ```
   */
  readonly embeddings: Embeddings<TModels>;

  /**
   * Models resource
   *
   * @example
   * ```typescript
   * const models = await client.models.list();
   * console.log(models.data.map(m => m.id));
   * ```
   */
  readonly models: Models;

  /**
   * Agents resource
   */
  readonly agents: Agents;

  /** Authenticated user and organization resolution. */
  readonly identity: Identity;

  /**
   * Datasets resource
   */
  readonly datasets: Datasets;

  /**
   * Evaluations resource
   */
  readonly evaluations: Evaluations;

  /**
   * Memory resource
   *
   * @example
   * ```typescript
   * const collections = await client.memory.collections.list();
   * const results = await client.memory.collections.query('my-coll', { query: 'hello' });
   * ```
   */
  readonly memory: Memory;

  /**
   * Audio resource (TTS, transcription, translation)
   *
   * @example
   * ```typescript
   * const audio = await client.audio.speech.create({
   *   model: 'tts-1', input: 'Hello!', voice: 'alloy',
   * });
   * ```
   */
  readonly audio: Audio;

  /**
   * Images resource (generation, editing, variations)
   */
  readonly images: Images;

  /**
   * Content moderation resource
   */
  readonly moderations: Moderations;

  /**
   * Document reranking resource
   */
  readonly rerank: Rerank;

  /**
   * Responses API resource (agentic orchestration)
   */
  readonly responses: Responses;

  /**
   * Observability resource (metrics, sessions, users, outcomes)
   */
  readonly observability: Observability;

  /**
   * Traces resource (list/stream traces, span trees, rich traces,
   * scores, performance breakdowns, resource utilization)
   */
  readonly traces: Traces;

  /**
   * Scores resource (submit and query evaluation scores)
   */
  readonly scores: Scores;

  /**
   * Channels resource (Slack, Discord, etc. channel bindings)
   */
  readonly channels: Channels;

  /** @internal The underlying Connect client */
  readonly _client: Client<typeof GatewayService>;

  /** @internal Client options */
  private readonly _options: Required<
    Pick<EverstackOptions, "apiKey" | "baseUrl">
  > &
    Omit<EverstackOptions, "apiKey" | "baseURL">;

  /**
   * Creates a new Everstack client
   *
   * @param options - Client configuration options
   *
   * @example
   * ```typescript
   * const client = new Everstack({
   *   apiKey: 'pk_...',
   *   provider: '@openai',
   * });
   * ```
   */
  constructor(options: EverstackOptions) {
    this._options = {
      ...options,
      baseUrl: options.baseUrl ?? EVERSTACK_GATEWAY_URL,
    };

    const transportOptions: TransportOptions = {
      baseUrl: this._options.baseUrl,
      apiKey: this._options.apiKey,
      provider: this._options.provider,
      orgId: this._options.orgId,
      userId: this._options.userId,
      headers: this._options.headers,
    };

    const transport = createTransport(transportOptions);
    this._client = createClient(GatewayService, transport);

    const agentsClient = createClient(AgentsService, transport);
    const identityClient = createClient(CLIService, transport);
    const channelsClient = createClient(ChannelsService, transport);
    const datasetsClient = createClient(DatasetService, transport);
    const evaluationsClient = createClient(EvalService, transport);
    const observabilityClient = createClient(ObservabilityService, transport);
    const scoresClient = createClient(ScoreService, transport);
    const tracesClient = createClient(TracesService, transport);

    // Initialize resources
    this.chat = new Chat<TModels>(this._client);
    this.embeddings = new Embeddings<TModels>(this._client);
    this.models = new Models(this._client);
    this.agents = new Agents(agentsClient, this._options.orgId, {
      baseUrl: this._options.baseUrl,
      apiKey: this._options.apiKey,
      tenantId: this._options.orgId,
      headers: this._options.headers,
    });
    this.identity = new Identity(identityClient);
    this.channels = new Channels(channelsClient);
    this.datasets = new Datasets(datasetsClient);
    this.evaluations = new Evaluations(evaluationsClient);
    this.memory = new Memory({
      baseUrl: this._options.baseUrl,
      apiKey: this._options.apiKey,
      tenantId: this._options.orgId,
      headers: this._options.headers,
    });
    this.audio = new Audio(this._client);
    this.images = new Images(this._client);
    this.moderations = new Moderations(this._client);
    this.rerank = new Rerank(this._client);
    this.responses = new Responses(this._client);
    this.scores = new Scores(scoresClient);
    this.observability = new Observability(observabilityClient);
    this.traces = new Traces(tracesClient, {
      baseUrl: this._options.baseUrl,
      apiKey: this._options.apiKey,
      headers: this._options.headers,
    });
  }

  /**
   * Report a caught error so it surfaces as an Issue. Convenience delegate for
   * {@link Traces.captureException}.
   *
   * @example
   * ```typescript
   * try {
   *   ...
   * } catch (err) {
   *   client.captureException(err, { provider: "openai", model: "gpt-4o" });
   * }
   * ```
   */
  captureException(error: unknown, options?: CaptureOptions) {
    return this.traces.captureException(error, options);
  }

  /**
   * Report a free-form failure message as an Issue (ERROR level). Convenience
   * delegate for {@link Traces.captureMessage}.
   */
  captureMessage(
    message: string,
    options?: CaptureOptions & {
      level?: "DEBUG" | "DEFAULT" | "WARNING" | "ERROR";
    },
  ) {
    return this.traces.captureMessage(message, options);
  }

  /**
   * Creates a new Everstack client and fetches available models
   *
   * This async factory method fetches the user's available models,
   * enabling type-safe model selection based on what the user has access to.
   *
   * @param options - Client configuration options
   * @returns A Everstack client with model types narrowed to available models
   *
   * @example
   * ```typescript
   * const client = await Everstack.init({
   *   apiKey: 'pk_...',
   * });
   *
   * // Autocomplete shows only user's available models
   * const response = await client.chat.completions.create({
   *   model: '@openai/gpt-4o',
   *   messages: [{ role: 'user', content: 'Hello!' }],
   * });
   * ```
   */
  static async init<TModels extends string = string>(
    options: EverstackOptions,
  ): Promise<Everstack<TModels>> {
    const client = new Everstack<TModels>(options);

    // Fetch available models to validate connection
    // In a future version, we could use this to narrow the type
    await client.models.list();

    return client;
  }

  /**
   * Get the base URL of the gateway
   */
  get baseURL(): string {
    return this._options.baseUrl;
  }

  /**
   * Get the API key (masked for security)
   */
  get apiKey(): string {
    const key = this._options.apiKey;
    if (key.length <= 8) return "****";
    return `${key.slice(0, 4)}...${key.slice(-4)}`;
  }
}

/**
 * Default export for convenient importing
 *
 * @example
 * ```typescript
 * import Everstack from '@everstack/everstack';
 * ```
 */
export default Everstack;
